package main

import (
	"fmt"
	"log"
	"os"

	"github.com/corbaltcode/go-libraries/migrations"
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

	err := migrations.SchemaTest(&cfg, allMigrations)
	if err != nil {
		log.Fatalf("Schema test failed: %s", err)
	}

	log.Printf("Schema test succeeded!")
}
