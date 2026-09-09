package core

import (
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"adaptive-vocabulary/migrations"
)

type MigrationInfo struct {
	Version int
	Name    string
	Content string
}

func RunMigrations(db *sql.DB) (int, error) {
	initQuery := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at INTEGER NOT NULL
		);
	`
	if _, err := db.Exec(initQuery); err != nil {
		return 0, fmt.Errorf("create schema_migrations table: %w", err)
	}

	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return 0, fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]bool)
	maxAppliedVersion := 0
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return 0, fmt.Errorf("scan migration version: %w", err)
		}
		applied[v] = true
		if v > maxAppliedVersion {
			maxAppliedVersion = v
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate migration rows: %w", err)
	}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return 0, fmt.Errorf("read embedded migrations dir: %w", err)
	}

	var toApply []MigrationInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) < 2 {
			continue
		}
		v, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, fmt.Errorf("invalid migration version in filename %q: %w", entry.Name(), err)
		}

		content, err := fs.ReadFile(migrations.FS, entry.Name())
		if err != nil {
			return 0, fmt.Errorf("read embedded migration file %q: %w", entry.Name(), err)
		}

		toApply = append(toApply, MigrationInfo{
			Version: v,
			Name:    entry.Name(),
			Content: string(content),
		})
	}

	sort.Slice(toApply, func(i, j int) bool {
		return toApply[i].Version < toApply[j].Version
	})

	for _, m := range toApply {
		if applied[m.Version] {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return 0, fmt.Errorf("begin migration tx for %s: %w", m.Name, err)
		}

		if _, err := tx.Exec(m.Content); err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("execute migration %s: %w", m.Name, err)
		}

		nowMicro := time.Now().UTC().UnixNano() / 1000
		insertMigration := `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`
		if _, err := tx.Exec(insertMigration, m.Version, m.Name, nowMicro); err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("record migration %s: %w", m.Name, err)
		}

		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit migration %s: %w", m.Name, err)
		}

		applied[m.Version] = true
		if m.Version > maxAppliedVersion {
			maxAppliedVersion = m.Version
		}
	}

	return maxAppliedVersion, nil
}
