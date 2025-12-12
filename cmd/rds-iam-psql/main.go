// rds-iam-psql.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
)

func main() {
	var (
		host       = flag.String("host", "", "RDS PostgreSQL endpoint hostname (no port, e.g. mydb.abc123.us-east-1.rds.amazonaws.com)")
		port       = flag.Int("port", 5432, "RDS PostgreSQL port (default 5432)")
		user       = flag.String("user", "", "Database user name")
		dbName     = flag.String("db", "", "Database name")
		region     = flag.String("region", "", "AWS region for the RDS instance (e.g. us-east-1). If empty, uses AWS config or tries to infer from host.")
		profile    = flag.String("profile", "", "Optional AWS shared config profile (e.g. dev)")
		psqlPath   = flag.String("psql", "psql", "Path to psql binary")
		sslMode    = flag.String("sslmode", "require", "PGSSLMODE for psql (e.g. require, verify-full)")
		searchPath = flag.String("search-path", "", "Optional PostgreSQL search_path to set (e.g. 'myschema,public')")
	)
	flag.Parse()

	if *host == "" || *user == "" || *dbName == "" {
		log.Fatalf("host, user, and db are required\n\nUsage example:\n  %s -host mydb.abc123.us-east-1.rds.amazonaws.com -port 5432 -user myuser -db mydb -search-path \"login,public\" -region us-east-1\n", os.Args[0])
	}

	ctx := context.Background()

	// Load AWS config (standard RDS/IAM auth expects your AWS creds, *not* the DB password).
	var cfg aws.Config
	var err error
	if *profile != "" {
		cfg, err = awsconfig.LoadDefaultConfig(ctx, awsconfig.WithSharedConfigProfile(*profile))
	} else {
		cfg, err = awsconfig.LoadDefaultConfig(ctx)
	}
	if err != nil {
		log.Fatalf("failed to load AWS config: %v", err)
	}

	awsRegion := *region
	if awsRegion == "" {
		awsRegion = cfg.Region
	}
	if awsRegion == "" {
		// Last resort: try to infer from the hostname if it looks like a standard RDS endpoint.
		if inferred := inferRegionFromHost(*host); inferred != "" {
			awsRegion = inferred
		}
	}

	if awsRegion == "" {
		log.Fatalf("AWS region is not set; pass -region or set AWS_REGION / configure your AWS profile")
	}

	endpointWithPort := fmt.Sprintf("%s:%d", *host, *port)

	// Generate the IAM auth token.
	authToken, err := auth.BuildAuthToken(ctx, endpointWithPort, awsRegion, *user, cfg.Credentials)
	if err != nil {
		log.Fatalf("failed to build RDS IAM auth token: %v", err)
	}

	// Prepare psql command. We pass the token through PGPASSWORD and SSL mode via PGSSLMODE.
	cmd := exec.Command(
		*psqlPath,
		"--host", *host,
		"--port", fmt.Sprintf("%d", *port),
		"--username", *user,
		"--dbname", *dbName,
	)

	// Attach stdio so it behaves like an interactive shell.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Inherit existing env and add PG vars.
	env := os.Environ()
	env = append(env,
		"PGPASSWORD="+authToken,
		"PGSSLMODE="+*sslMode,
	)

	// If a search path is provided, wire it through PGOPTIONS.
	if sp := strings.TrimSpace(*searchPath); sp != "" {
		// Build our addition: one -c flag.
		add := "-c search_path=" + sp

		// Check if PGOPTIONS already exists; if so, append.
		found := false
		for i, e := range env {
			if strings.HasPrefix(e, "PGOPTIONS=") {
				current := strings.TrimPrefix(e, "PGOPTIONS=")
				if strings.TrimSpace(current) == "" {
					env[i] = "PGOPTIONS=" + add
				} else {
					env[i] = "PGOPTIONS=" + current + " " + add
				}
				found = true
				break
			}
		}
		if !found {
			env = append(env, "PGOPTIONS="+add)
		}
	}

	cmd.Env = env

	if err := cmd.Run(); err != nil {
		// psql will print its own error messages; just propagate the exit code.
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		log.Fatalf("failed to run psql: %v", err)
	}
}

// inferRegionFromHost tries to pull the AWS region out of a typical RDS hostname like
// "mydb.abc123.us-east-1.rds.amazonaws.com". If it can't, it returns "".
func inferRegionFromHost(host string) string {
	parts := strings.Split(host, ".")
	for i := 0; i < len(parts); i++ {
		if parts[i] == "rds" && i > 0 {
			return parts[i-1]
		}
	}
	return ""
}
