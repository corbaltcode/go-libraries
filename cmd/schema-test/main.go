package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/corbaltcode/go-libraries/migrations"
	"github.com/corbaltcode/go-libraries/pgutils"
	_ "github.com/lib/pq"
)

func main() {
	cfg := migrations.PostgresConfig{
		Host:     "localhost",
		Port:     os.Getenv("SCHEMA_TEST_POSTGRES_PORT"),
		Database: "postgres",
		User:     "postgres",
		Password: "postgres",
	}
	if cfg.Port == "" {
		fmt.Fprintf(os.Stderr, "SCHEMA_TEST_POSTGRES_PORT env variable must be set\n")
		os.Exit(1)
	}

	commonSchema := "common"

	postgresConnectionString := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database)
	connectionStringProvider, err := pgutils.NewConnectionStringProviderFromURLString(context.Background(), postgresConnectionString)
	if err != nil {
		log.Fatalf("NewConnectionStringProviderFromURLString: %v", err)
	}
	dbWithSearchPath, err := pgutils.ConnectDB(pgutils.ToConnector(pgutils.WithSchemaSearchPath(connectionStringProvider, commonSchema)))
	if err != nil {
		log.Fatalf("ConnectDB: %v", err)
	}

	commonSetup := func() error {
		err = migrations.EnsureSchema(dbWithSearchPath, commonSchema)
		if err != nil {
			return fmt.Errorf("EnsureSchema: %v", err)
		}
		return migrations.Migrate(dbWithSearchPath, commonMigrations)
	}

	err = migrations.SchemaTestWithSetup(&cfg, allMigrations, commonSetup)
	if err != nil {
		log.Fatalf("First schema test failed: %s", err)
	}

	// Database must be empty before calling SchemaTest a second time.
	dbWithSearchPath.MustExec(fmt.Sprintf("DROP SCHEMA %s CASCADE", commonSchema))

	err = migrations.SchemaTestWithSetup(&cfg, allMigrations, commonSetup)
	if err != nil {
		log.Fatalf("Second schema test failed: %s", err)
	}

	log.Printf("Schema tests succeeded!")
}
