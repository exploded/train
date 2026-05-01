package db

import (
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed schema.sql
var schemaSQL string

// columnAdds lists ALTER TABLE ADD COLUMN migrations for tables that already
// exist on prior installs. SQLite's CREATE TABLE IF NOT EXISTS does not add
// new columns to a pre-existing table, so each new column needs an entry
// here. The check uses PRAGMA table_info to stay idempotent.
var columnAdds = []struct {
	table, column, decl string
}{
	{"workouts", "completed_at", "TEXT"},
}

func Migrate(d *sql.DB) error {
	if _, err := d.Exec(schemaSQL); err != nil {
		return err
	}
	for _, m := range columnAdds {
		exists, err := columnExists(d, m.table, m.column)
		if err != nil {
			return fmt.Errorf("check %s.%s: %w", m.table, m.column, err)
		}
		if exists {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", m.table, m.column, m.decl)
		if _, err := d.Exec(stmt); err != nil {
			return fmt.Errorf("add column %s.%s: %w", m.table, m.column, err)
		}
	}
	return nil
}

func columnExists(d *sql.DB, table, column string) (bool, error) {
	rows, err := d.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
