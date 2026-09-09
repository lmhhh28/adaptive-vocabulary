// dependencycheck verifies the frozen dependency baseline; it is not the app server.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"
	_ "modernc.org/sqlite"
)

func main() {
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check() error {
	dir, err := os.MkdirTemp("", "avocab-dependencies-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := sql.Open("sqlite", filepath.Join(dir, "app.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var version, sourceID, journal string
	if err := db.QueryRow("SELECT sqlite_version(), sqlite_source_id()").Scan(&version, &sourceID); err != nil {
		return err
	}
	var major, minor, patch int
	if n, _ := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); n != 3 || major*1000000+minor*1000+patch < 3051003 {
		return fmt.Errorf("SQLite %s is below 3.51.3", version)
	}
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&journal); err != nil {
		return err
	}
	if journal != "wal" {
		return fmt.Errorf("journal mode: %s", journal)
	}
	for pragma, expected := range map[string]int{"foreign_keys": 1, "busy_timeout": 5000, "synchronous": 2} {
		var actual int
		if err := db.QueryRow("PRAGMA " + pragma).Scan(&actual); err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf("%s: got %d, want %d", pragma, actual, expected)
		}
	}
	if _, err := db.Exec("CREATE TABLE sample (id INTEGER PRIMARY KEY, value TEXT NOT NULL); INSERT INTO sample VALUES (1, 'committed')"); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO sample VALUES (2, 'rolled back')"); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Rollback(); err != nil {
		return err
	}
	backup := filepath.Join(dir, "backup.db")
	if _, err := db.Exec("VACUUM INTO ?", backup); err != nil {
		return err
	}
	snapshot, err := sql.Open("sqlite", backup)
	if err != nil {
		return err
	}
	defer snapshot.Close()
	var count int
	if err := snapshot.QueryRow("SELECT count(*) FROM sample").Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("snapshot/rollback count: %d", count)
	}
	var integrity string
	if err := snapshot.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("snapshot integrity: %s", integrity)
	}

	p := fsrs.DefaultParam()
	var policy struct {
		FSRS struct {
			Weights    fsrs.Weights `json:"weights"`
			Retention  float64      `json:"desired_retention"`
			Maximum    float64      `json:"maximum_interval_days"`
			ShortTerm  bool         `json:"enable_short_term"`
			Fuzz       bool         `json:"enable_fuzz"`
			Learning   []float64    `json:"learning_steps_minutes"`
			Relearning []float64    `json:"relearning_steps_minutes"`
		} `json:"fsrs"`
	}
	raw, err := os.ReadFile("config/policy-v2.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		return err
	}
	f := policy.FSRS
	if p.W != f.Weights || p.RequestRetention != f.Retention || p.MaximumInterval != f.Maximum || p.EnableShortTerm != f.ShortTerm || p.EnableFuzz != f.Fuzz || !reflect.DeepEqual(p.LearningSteps, f.Learning) || !reflect.DeepEqual(p.RelearningSteps, f.Relearning) {
		return fmt.Errorf("FSRS built-in defaults differ from frozen parameter snapshot")
	}
	scheduler := fsrs.NewFSRS(p)
	start := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	card := fsrs.NewCard(start)
	replay := fsrs.NewCard(start)
	ratings := []fsrs.Rating{fsrs.Again, fsrs.Good, fsrs.Good}
	offsets := []time.Duration{0, 5 * time.Minute, 48 * time.Hour}
	for i, rating := range ratings {
		next, err := scheduler.Next(card, start.Add(offsets[i]), rating)
		if err != nil {
			return err
		}
		card = next.Card
		next, err = scheduler.Next(replay, start.Add(offsets[i]), rating)
		if err != nil {
			return err
		}
		replay = next.Card
	}
	if !reflect.DeepEqual(card, replay) || card.Stability <= 0 || card.Difficulty <= 0 {
		return fmt.Errorf("FSRS replay mismatch or invalid state")
	}
	if !strings.HasPrefix(runtime.Version(), "go1.27.1") {
		return fmt.Errorf("unexpected Go runtime: %s", runtime.Version())
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"status": "ok", "go": runtime.Version(), "sqlite_version": version, "sqlite_source_id": sourceID,
		"journal_mode": journal, "rollback_and_snapshot": "ok", "fsrs_reviews": len(ratings), "fsrs_replay": "identical", "fsrs_parameters": p,
	})
}
