package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/corbaltcode/go-libraries/pgutils"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(
			os.Stderr,
			"expected exactly one positional RDS IAM connection URL argument, got %d\nUsage:\n  %s 'postgres+rds-iam://user@host:5432/db'\n",
			len(os.Args)-1,
			os.Args[0],
		)
		os.Exit(2)
	}
	rawURL := os.Args[1]

	ctx := context.Background()

	restoreLogger := suppressStdLogger()
	defer restoreLogger()

	connectionStringProvider, err := pgutils.NewConnectionStringProviderFromURLString(ctx, rawURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create connection string provider: %v\n", err)
		os.Exit(1)
	}

	dsnWithToken, err := connectionStringProvider.ConnectionString(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get connection string from provider: %v\n", err)
		os.Exit(1)
	}

	// Print only the final DSN to stdout for command-substitution in scripts.
	fmt.Fprintln(os.Stdout, dsnWithToken)
}

func suppressStdLogger() func() {
	prevOutput := log.Writer()
	prevFlags := log.Flags()
	prevPrefix := log.Prefix()

	log.SetOutput(io.Discard)
	log.SetFlags(0)
	log.SetPrefix("")

	return func() {
		log.SetOutput(prevOutput)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	}
}
