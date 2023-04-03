package migrations

import (
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

type migrationRow struct {
	index int
	name  string
}

type migrationTestCase struct {
	name                string
	allMigrations       []migrationRow
	inDatabase          []migrationRow
	firstUnappliedIndex int
	expectedError       error
}

var migrationTestCases []*migrationTestCase = []*migrationTestCase{
	{
		name:                "No migrations in DB, no migrations to do",
		firstUnappliedIndex: 0,
	},
	{
		name: "Some migrations in DB, no migrations to do",
		allMigrations: []migrationRow{
			{0, "a migration"},
			{1, "another migration"},
		},
		inDatabase: []migrationRow{
			{0, "a migration"},
			{1, "another migration"},
		},
		firstUnappliedIndex: 2,
	},
	{
		name: "Different name in database",
		allMigrations: []migrationRow{
			{0, "a migration"},
			{1, "another migration"},
		},
		inDatabase: []migrationRow{
			{0, "a migration"},
			{1, "a different migration"},
		},
		expectedError: errors.New(`Migration 1: expected name "another migration" but was "a different migration" in database`),
	},
	{
		name: "Unexpected migrations in database",
		allMigrations: []migrationRow{
			{0, "a migration"},
		},
		inDatabase: []migrationRow{
			{0, "a migration"},
			{1, "a second migration"},
			{2, "a third migration"},
		},
		expectedError: errors.New(`Cannot verify migration 1 ("a second migration") in database: no such migration`),
	},
	{
		name: "Migration missing",
		allMigrations: []migrationRow{
			{0, "a migration"},
			{1, "another migration"},
			{2, "a third migration"},
		},
		inDatabase: []migrationRow{
			{0, "a migration"},
			{2, "a third migration"},
		},
		expectedError: errors.New(`Migration 1 ("another migration") was skipped in the database`),
	},
	{
		name: "Invalid index",
		inDatabase: []migrationRow{
			{-1, "some other migration"},
		},
		expectedError: errors.New(`Negative or duplicate migration index -1 in database`),
	},
}

func TestVerifyMigrations(t *testing.T) {
	db := sqlx.MustOpen("sqlite", ":memory:")
	_, err := db.Exec(createMigrationsTableSQL)
	if err != nil {
		t.Fatalf("Error creating migrations table: %s", err)
	}
	for _, tc := range migrationTestCases {
		t.Run(tc.name, func(t *testing.T) {
			migrations := make([]NamedMigration, len(tc.allMigrations))
			for idx, m := range tc.allMigrations {
				migrations[idx] = NamedMigration{
					Name:      m.name,
					Migration: StaticMigration{},
				}
			}
			tx, err := db.Beginx()
			if err != nil {
				t.Fatalf("Error starting transaction: %s", err)
			}
			defer tx.Rollback()
			for _, m := range tc.inDatabase {
				_, err := tx.Exec(`INSERT INTO migration ("index", name) VALUES ($1, $2)`, m.index, m.name)
				if err != nil {
					t.Fatalf("Error recording migration in test setup: %s", err)
				}
			}
			firstUnappliedIndex, err := verifyMigrations(tx, migrations)
			if ((err == nil) != (tc.expectedError == nil)) || (err != nil && err.Error() != tc.expectedError.Error()) {
				t.Fatalf("Expected error=%v but got error=%v", tc.expectedError, err)
			}
			if err == nil && firstUnappliedIndex != tc.firstUnappliedIndex {
				t.Fatalf("Expected firstUnappliedIndex=%v but got firstUnappliedIndex=%v", tc.firstUnappliedIndex, firstUnappliedIndex)
			}
		})
	}
}
