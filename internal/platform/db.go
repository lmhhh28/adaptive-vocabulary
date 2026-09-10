package platform

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
	ReadOnly *sql.DB
	Version  string
	SourceID string
}

func (d *DB) Close() error {
	var errs []error
	if d.ReadOnly != nil {
		if err := d.ReadOnly.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if d.DB != nil {
		if err := d.DB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close db: %v", errs)
	}
	return nil
}

func OpenDB(dataDir string) (*DB, error) {
	dbPath := filepath.Join(dataDir, "app.db")
	dsn := fmt.Sprintf("%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)", dbPath)

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	// Single writer constraint as required by the modular monolith specification
	sqlDB.SetMaxOpenConns(1)

	var version, sourceID, journal string
	if err := sqlDB.QueryRow("SELECT sqlite_version(), sqlite_source_id()").Scan(&version, &sourceID); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("query sqlite version: %w", err)
	}

	var major, minor, patch int
	if n, _ := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); n != 3 || major*1000000+minor*1000+patch < 3051003 {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("SQLite %s is below required baseline 3.51.3", version)
	}

	if err := sqlDB.QueryRow("PRAGMA journal_mode=WAL").Scan(&journal); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("enable WAL journal mode: %w", err)
	}
	if journal != "wal" {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("unexpected journal mode: got %q, want wal", journal)
	}

	for pragma, expected := range map[string]int{"foreign_keys": 1, "busy_timeout": 5000, "synchronous": 2} {
		var actual int
		if err := sqlDB.QueryRow("PRAGMA " + pragma).Scan(&actual); err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("query PRAGMA %s: %w", pragma, err)
		}
		if actual != expected {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("PRAGMA %s mismatch: got %d, want %d", pragma, actual, expected)
		}
	}

	// Read connection pool with query_only=1 for concurrent read scalability under WAL
	readDSN := fmt.Sprintf("%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)&_pragma=query_only(1)", dbPath)
	readDB, err := sql.Open("sqlite", readDSN)
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open sqlite read database: %w", err)
	}
	readDB.SetMaxOpenConns(4)

	return &DB{
		DB:       sqlDB,
		ReadOnly: readDB,
		Version:  version,
		SourceID: sourceID,
	}, nil
}
