package migrations

import (
	"fmt"
	"log"

	"github.com/jmoiron/sqlx"
)

// A Migration performs a schema migration on the database.
type Migration interface {
	// DoMigration runs the actual migration. All database
	// access must be through the transaction given as an
	// argument. It should not manually commit or roll
	// back the transaction.
	DoMigration(*sqlx.Tx) error
}

type NamedMigration struct {
	Name      string
	Migration Migration
	// Does the opposite of the above Migration
	Reverse Migration
}

// A Migration that is just a list of SQL statements to perform. The
// DoMigration method will execute all the SQL.
type StaticMigration []string

// A CustomMigration is cast from a function to create a Migration
// which uses the function as its DoMigration method.
type CustomMigration func(*sqlx.Tx) error

func (m CustomMigration) DoMigration(tx *sqlx.Tx) error {
	return m(tx)
}

func (m StaticMigration) DoMigration(tx *sqlx.Tx) error {
	for _, sql := range m {
		_, err := tx.Exec(sql)
		if err != nil {
			return err
		}
	}
	return nil
}

const createMigrationsTableSQL = `
	CREATE TABLE migration (
		id serial PRIMARY KEY,
		"index" integer,
		name text,
		applied_at timestamp with time zone DEFAULT current_timestamp,
		UNIQUE ("index")
	)`

func ensureMigrationsTableExists(db *sqlx.DB) error {
	row := db.QueryRow("SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = 'migration' AND table_schema = current_schema())")
	exists := false
	err := row.Scan(&exists)
	if err != nil {
		return fmt.Errorf("Error checking for migration table: %w", err)
	}
	if !exists {
		_, err := db.Exec(createMigrationsTableSQL)
		if err != nil {
			return fmt.Errorf("Error creating migrations table: %w", err)
		}
	}
	return nil
}

func verifyMigrations(tx *sqlx.Tx, migrations []NamedMigration) (firstUnappliedMigrationIndex int, err error) {
	firstUnappliedMigrationIndex = 0
	var appliedMigrationIndex int
	var appliedMigrationName string
	rows, err := tx.Query(`SELECT "index", name FROM migration ORDER BY "index" ASC`)
	if err != nil {
		return -1, fmt.Errorf("Error fetching migrations: %w", err)
	}
	for rows.Next() {
		err := rows.Scan(&appliedMigrationIndex, &appliedMigrationName)
		if err != nil {
			return -1, fmt.Errorf("Error fetching migrations: %w", err)
		}
		if appliedMigrationIndex < firstUnappliedMigrationIndex {
			return -1, fmt.Errorf("Negative or duplicate migration index %d in database", appliedMigrationIndex)
		}
		if appliedMigrationIndex >= len(migrations) {
			return -1, fmt.Errorf("Cannot verify migration %d (%q) in database: no such migration", appliedMigrationIndex, appliedMigrationName)
		}
		nextMigrationName := migrations[firstUnappliedMigrationIndex].Name
		if appliedMigrationIndex > firstUnappliedMigrationIndex {
			return -1, fmt.Errorf("Migration %d (%q) was skipped in the database", firstUnappliedMigrationIndex, nextMigrationName)
		}
		if appliedMigrationName != migrations[firstUnappliedMigrationIndex].Name {
			return -1, fmt.Errorf("Migration %d: expected name %q but was %q in database", appliedMigrationIndex, nextMigrationName, appliedMigrationName)
		}
		firstUnappliedMigrationIndex++
	}
	err = rows.Err()
	if err != nil {
		return -1, fmt.Errorf("Error fetching existing migrations: %w", err)
	}
	return firstUnappliedMigrationIndex, nil
}

func migrateOne(db *sqlx.DB, migrations []NamedMigration) (bool, error) {
	tx, err := db.Beginx()
	if err != nil {
		return false, fmt.Errorf("Error starting migrations transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			err := tx.Rollback()
			if err != nil {
				log.Printf("Error rolling back migrations transaction: %s", err)
			}
		}
	}()

	_, err = tx.Exec("LOCK TABLE migration")
	if err != nil {
		return false, fmt.Errorf("Error locking migration table: %w", err)
	}

	firstUnappliedIndex, err := verifyMigrations(tx, migrations)
	if err != nil {
		return false, err
	}
	if firstUnappliedIndex >= len(migrations) {
		return false, nil
	}

	migration := migrations[firstUnappliedIndex]
	log.Printf("Performing migration %d (%q)", firstUnappliedIndex, migration.Name)
	err = migrations[firstUnappliedIndex].Migration.DoMigration(tx)
	if err != nil {
		return false, fmt.Errorf("Error performing migration %d (%q): %w", firstUnappliedIndex, migration.Name, err)
	}
	_, err = tx.Exec(`INSERT INTO migration ("index", name) VALUES ($1, $2)`, firstUnappliedIndex, migration.Name)
	if err != nil {
		return false, fmt.Errorf("Error recording migration %d (%q): %w", firstUnappliedIndex, migration.Name, err)
	}

	committed = true
	err = tx.Commit()
	if err != nil {
		return false, fmt.Errorf("Error committing migrations: %w", err)
	}
	return true, nil
}

func rollbackOne(db *sqlx.DB, migrations []NamedMigration, rollBackThroughIndex int) (rolledBackIndex int, err error) {
	tx, err := db.Beginx()
	if err != nil {
		return -1, fmt.Errorf("Error starting migrations transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			err := tx.Rollback()
			if err != nil {
				log.Printf("Error rolling back migrations transaction: %s", err)
			}
		}
	}()

	_, err = tx.Exec("LOCK TABLE migration")
	if err != nil {
		return -1, fmt.Errorf("Error locking migration table: %w", err)
	}

	firstUnappliedIndex, err := verifyMigrations(tx, migrations)
	if err != nil {
		return -1, err
	}

	if rollBackThroughIndex < 0 {
		return -1, fmt.Errorf("Invalid target index %d", rollBackThroughIndex)
	}
	if rollBackThroughIndex >= firstUnappliedIndex {
		return -1, fmt.Errorf("Migration %d has not been applied yet", rollBackThroughIndex)
	}

	index := firstUnappliedIndex - 1
	migration := migrations[index]
	if migration.Reverse == nil {
		return -1, fmt.Errorf("No Reverse for migration %d (%q)", index, migration.Name)
	}
	log.Printf("Reversing migration %d (%q)", index, migration.Name)
	err = migrations[index].Reverse.DoMigration(tx)
	if err != nil {
		return -1, fmt.Errorf("Error reversing migration %d (%q): %w", index, migration.Name, err)
	}
	_, err = tx.Exec(`DELETE FROM migration WHERE "index"=$1`, index)
	if err != nil {
		return -1, fmt.Errorf("Error deleting migration row %d (%q): %w", index, migration.Name, err)
	}

	committed = true
	err = tx.Commit()
	if err != nil {
		return -1, fmt.Errorf("Error committing migrations: %w", err)
	}
	return index, nil
}

// Rollback runs the Reverse migrations for all the input migrations with index >= rollBackThroughIndex.
// The input migrations must include all migrations, not just the ones to roll back.
func Rollback(db *sqlx.DB, migrations []NamedMigration, rollBackThroughIndex int) error {
	err := ensureMigrationsTableExists(db)
	if err != nil {
		return err
	}
	for {
		rolledBackIndex, err := rollbackOne(db, migrations, rollBackThroughIndex)
		if err != nil {
			return err
		}
		if rolledBackIndex == rollBackThroughIndex {
			return nil
		}
	}
}

// Migrate does the following:
// 1. Verifies that the `migration` table exists, and creates it if it does not.
// 2. Verifies that the existing migrations recorded in the database match (by name and order) the migrations given as the argument.
// 3. Performs any migrations that are not yet recorded in the database.
func Migrate(db *sqlx.DB, migrations []NamedMigration) error {
	err := ensureMigrationsTableExists(db)
	if err != nil {
		return err
	}
	for {
		migrated, err := migrateOne(db, migrations)
		if err != nil {
			return err
		}
		if !migrated {
			return nil
		}
	}
}

func EnsureSchema(db *sqlx.DB, schemaName string) error {
	q := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName)
	_, err := db.Exec(q)
	if err != nil {
		return fmt.Errorf("Error ensuring schema %q: %w", schemaName, err)
	}

	return nil
}
