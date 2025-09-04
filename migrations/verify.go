package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"

	"github.com/google/go-cmp/cmp"
	"github.com/jmoiron/sqlx"
)

type PostgresConfig struct {
	Host, Port, Database, User, Password string
}

func dump(c *PostgresConfig) ([]byte, error) {
	pgpass := fmt.Sprintf("%s:%s:%s:%s:%s", c.Host, c.Port, c.Database, c.User, c.Password)
	tempDir, err := os.MkdirTemp(os.TempDir(), "pgdump")
	if err != nil {
		return nil, fmt.Errorf("Error creating temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)
	passFilePath := path.Join(tempDir, ".pgpass")
	err = os.WriteFile(passFilePath, []byte(pgpass), 0600)
	if err != nil {
		return nil, fmt.Errorf("Error writing pgpass: %w", err)
	}
	cmd := exec.Command("pg_dump",
		"-s", // schema only
		"-h", c.Host,
		"-p", c.Port,
		"-U", c.User,
		// if not specified, will be random, so not repeatable
		"--restrict-key=key",
		c.Database)
	cmd.Dir = tempDir
	cmd.Env = append(cmd.Env, "PGPASSFILE="+passFilePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Error calling pg_dump: %s. Output:\n%s", err, out)
	}
	return out, err
}

func verifyNoTables(db *sqlx.DB) error {
	// Based on the query run for the "\d" command in psql
	// (as revealed when started with -E flag).
	q := `SELECT 1 FROM pg_catalog.pg_class c
		LEFT JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','p','v','m','S','f','')
		AND n.nspname <> 'pg_catalog'
		AND n.nspname !~ '^pg_toast'
		AND n.nspname <> 'information_schema'`
	var ignored int
	err := db.QueryRow(q).Scan(&ignored)
	if err == sql.ErrNoRows {
		// Expected
		return nil
	} else if err != nil {
		return fmt.Errorf("Error checking for existing tables: %w", err)
	}
	return errors.New("Existing tables found. You must run SchemaTest on an empty database.")
}

func migrateAndRollback(emptyDBConfig *PostgresConfig, db *sqlx.DB, allMigrations []NamedMigration, migrateToIndex, rollbackThroughIndex int, repeatForward bool) error {
	beforeMigrate, err := dump(emptyDBConfig)
	if err != nil {
		return fmt.Errorf("Error calling pg_dump: %s", err)
	}
	err = Migrate(db, allMigrations[:migrateToIndex+1])
	if err != nil {
		return fmt.Errorf("Migrate to %q failed: %s", allMigrations[migrateToIndex].Name, err)
	}
	afterMigrate, err := dump(emptyDBConfig)
	if err != nil {
		return fmt.Errorf("Error calling pg_dump: %s", err)
	}
	err = Rollback(db, allMigrations, rollbackThroughIndex)
	if err != nil {
		return fmt.Errorf("Rollback through %q failed: %s", allMigrations[rollbackThroughIndex].Name, err)
	}
	afterRollback, err := dump(emptyDBConfig)
	if err != nil {
		return fmt.Errorf("Error calling pg_dump: %s", err)
	}
	if string(beforeMigrate) != string(afterRollback) {
		fmt.Printf("%s\n", cmp.Diff(string(beforeMigrate), string(afterRollback)))
		return fmt.Errorf("Dump after rollback through %q did not match the dump before the migration", allMigrations[rollbackThroughIndex].Name)
	}
	if repeatForward {
		err = Migrate(db, allMigrations[:migrateToIndex+1])
		if err != nil {
			return fmt.Errorf("Migration to %q failed: %s", allMigrations[migrateToIndex].Name, err)
		}
		afterMigrateAgain, err := dump(emptyDBConfig)
		if err != nil {
			return fmt.Errorf("Error calling pg_dump: %s", err)
		}
		if string(afterMigrate) != string(afterMigrateAgain) {
			fmt.Printf("%s\n", cmp.Diff(string(afterMigrate), string(afterMigrateAgain)))
			return fmt.Errorf("Dump after re-migration of %q did not match dump after first migration", allMigrations[migrateToIndex].Name)
		}
	}
	return err
}

// Schema test expects a new *empty* postgres database.
// It will:
//  1. Apply all migrations
//  2. Reverse all migrations
//  3. For each migration:
//     a. Apply the migration
//     b. Reverse the migration
//     c. Apply the migration again
//
// Before and after each step it will use pg_dump to dump the database schema.
// It will verify that:
// A. The schema is the same after reversing as before applying.
// B. (If re-applying) The schema is the same after applying as after re-applying.
//
// You must have `pg_dump` in your `PATH` to run this.
func SchemaTest(emptyDBConfig *PostgresConfig, allMigrations []NamedMigration) error {
	for _, v := range []string{
		emptyDBConfig.Host,
		emptyDBConfig.Port,
		emptyDBConfig.Database,
		emptyDBConfig.User,
		emptyDBConfig.Password,
	} {
		if v == "" {
			return errors.New("All PostgresConfig fields must be specified")
		}
	}
	postgresConnectionString := fmt.Sprintf(
		"host=%s port=%s password=%s sslmode=disable user=%s dbname=%s",
		emptyDBConfig.Host, emptyDBConfig.Port, emptyDBConfig.Password, emptyDBConfig.User, emptyDBConfig.Database)
	db, err := sqlx.Connect("postgres", postgresConnectionString)
	if err != nil {
		return fmt.Errorf("Error connecting: %s", err)
	}
	err = verifyNoTables(db)
	if err != nil {
		return err
	}
	err = Migrate(db, []NamedMigration{})
	if err != nil {
		return fmt.Errorf("Setting up migrations table failed: %s", err)
	}
	err = migrateAndRollback(emptyDBConfig, db, allMigrations, len(allMigrations)-1, 0, false)
	if err != nil {
		return err
	}
	for idx := range allMigrations {
		err := migrateAndRollback(emptyDBConfig, db, allMigrations, idx, idx, true)
		if err != nil {
			return err
		}
	}
	return nil
}
