package platform

import (
	"database/sql"

	"adaptive-vocabulary/internal/core"
)

type MigrationInfo = core.MigrationInfo

func RunMigrations(db *sql.DB) (int, error) {
	return core.RunMigrations(db)
}
