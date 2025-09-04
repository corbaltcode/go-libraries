package main

import "github.com/corbaltcode/go-libraries/migrations"

var allMigrations = []migrations.NamedMigration{
	{
		Name: "Create a table",
		Migration: migrations.StaticMigration([]string{
			`CREATE TABLE table1 (
				id serial PRIMARY KEY,
				f1 TEXT NULL
			)`,
		}),
		Reverse: migrations.StaticMigration([]string{
			`DROP TABLE table1`,
		}),
	},
	{
		Name: "Make field not-nullable",
		Migration: migrations.StaticMigration([]string{
			`ALTER TABLE table1 ALTER COLUMN f1 SET NOT NULL`,
		}),
		Reverse: migrations.StaticMigration([]string{
			`ALTER TABLE table1 ALTER COLUMN f1 DROP NOT NULL`,
		}),
	},
	{
		Name: "Create a dependent table",
		Migration: migrations.StaticMigration([]string{
			`CREATE TABLE table2 (
				id serial PRIMARY KEY,
				table1_id integer REFERENCES table1(id)
			)`,
		}),
		Reverse: migrations.StaticMigration([]string{
			`DROP TABLE table2`,
		}),
	},
	{
		Name: "Create a table with an enum type",
		Migration: migrations.StaticMigration([]string{
			`CREATE TYPE type1 AS ENUM (
				'type1va11',
				'type1val2'
			)`,
			`CREATE TABLE table3 (
				id serial PRIMARY KEY,
				v type1 NOT NULL
			)`,
		}),
		Reverse: migrations.StaticMigration([]string{
			`DROP TABLE table3`,
			`DROP TYPE type1`,
		}),
	},
	{
		Name: "Add a new value to the enum type",
		Migration: migrations.StaticMigration([]string{
			`ALTER TYPE type1 ADD VALUE 'type1val3'`,
		}),
		Reverse: migrations.StaticMigration([]string{
			`ALTER TYPE type1 RENAME TO type1_old`,
			`CREATE TYPE type1 AS ENUM (
			'type1va11',
			'type1val2'
		)`,
			`ALTER TABLE table3 ALTER COLUMN v TYPE type1 USING v::text::type1`,
			`DROP TYPE type1_old`,
		}),
	},
	{
		Name: "Create a view referencing a new enum value",
		Migration: migrations.StaticMigration([]string{
			`CREATE VIEW v AS SELECT * FROM table3 WHERE v = 'type1val3'`,
		}),
		Reverse: migrations.StaticMigration([]string{
			`DROP VIEW v`,
		}),
	},
}
