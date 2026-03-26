package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/corbaltcode/go-libraries/pgutils"
)

func main() {
	log.SetFlags(0) // no timestamps — keep output clean for CLI use

	if len(os.Args) != 2 || os.Args[1] == "-help" || os.Args[1] == "--help" || os.Args[1] == "-h" {
		fmt.Fprintf(os.Stderr,
			"Usage: %s 'postgres+rds-iam://user@host:5432/db'\n",
			filepath.Base(os.Args[0]),
		)
		os.Exit(2)
	}

	rawURL := os.Args[1]
	ctx := context.Background()

	connectionStringProvider, err := pgutils.NewConnectionStringProviderFromURLString(ctx, rawURL)
	if err != nil {
		log.Fatalf("failed to create connection string provider: %v", err)
	}

	dsnWithToken, err := connectionStringProvider.ConnectionString(ctx)
	if err != nil {
		log.Fatalf("failed to get connection string from provider: %v", err)
	}

	fmt.Println(dsnWithToken)
}
