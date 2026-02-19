package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/corbaltcode/go-libraries/pgutils"
)

func main() {
	var (
		host       = flag.String("host", "", "RDS PostgreSQL endpoint hostname (no port, e.g. mydb.abc123.us-east-1.rds.amazonaws.com)")
		port       = flag.Int("port", 5432, "RDS PostgreSQL port (default 5432)")
		user       = flag.String("user", "", "Database user name")
		dbName     = flag.String("db", "", "Database name")
		psqlPath   = flag.String("psql", "psql", "Path to psql binary")
		sslMode    = flag.String("sslmode", "require", "PGSSLMODE for psql (e.g. require, verify-full)")
		searchPath = flag.String("search-path", "", "Optional PostgreSQL search_path to set (e.g. 'myschema,public')")
	)
	flag.Parse()

	args := flag.Args()
	if len(args) > 1 {
		log.Fatalf("expected at most one positional connection URL argument, got %d", len(args))
	}

	connectionURLArg := ""
	if len(args) == 1 {
		connectionURLArg = args[0]
	}

	rawURL, usesIAM, err := buildRawURL(connectionURLArg, *host, *port, *user, *dbName)
	if err != nil {
		log.Fatalf("%v\n\nUsage examples:\n  %s -host mydb.abc123.us-east-1.rds.amazonaws.com -port 5432 -user myuser -db mydb -search-path \"login,public\"\n  %s 'postgres+rds-iam://myuser@mydb.abc123.us-east-1.rds.amazonaws.com:5432/mydb'\n", err, os.Args[0], os.Args[0])
	}

	ctx := context.Background()

	connectionStringProvider, err := pgutils.NewConnectionStringProviderFromURLString(ctx, rawURL)
	if err != nil {
		log.Fatalf("failed to create connection string provider: %v", err)
	}

	if usesIAM {
		if os.Getenv("AWS_REGION") == "" {
			log.Fatalf("AWS_REGION must be set for IAM auth")
		}

		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			log.Fatalf("failed to load AWS config: %v", err)
		}
		if err := printCallerIdentity(ctx, cfg); err != nil {
			log.Fatalf("AWS credentials check failed: %v", err)
		}
	}

	dsnWithToken, err := connectionStringProvider.ConnectionString(ctx)
	if err != nil {
		log.Fatalf("failed to get connection string from provider: %v", err)
	}

	parsedURL, err := url.Parse(dsnWithToken)
	if err != nil {
		log.Fatalf("failed to parse connection string from provider: %v", err)
	}

	password := ""
	if parsedURL.User != nil {
		var ok bool
		password, ok = parsedURL.User.Password()
		if ok {
			parsedURL.User = url.User(parsedURL.User.Username())
		}
	}

	// Pass DSN to psql without password in argv, and provide password via env.
	cmd := exec.Command(*psqlPath, parsedURL.String())

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	env := os.Environ()
	if password != "" {
		env = append(env, "PGPASSWORD="+password)
	}
	env = append(env, "PGSSLMODE="+*sslMode)

	if sp := strings.TrimSpace(*searchPath); sp != "" {
		add := "-c search_path=" + sp

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

	// Keep psql in the foreground process group. Swallow SIGINT in wrapper so
	// psql handles Ctrl-C directly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if err := cmd.Start(); err != nil {
		log.Fatalf("failed to start psql: %v", err)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	for {
		select {
		case sig := <-sigCh:
			switch sig {
			case os.Interrupt:
				continue
			case syscall.SIGTERM:
				if cmd.Process != nil {
					_ = cmd.Process.Signal(syscall.SIGTERM)
				}
			}
		case err := <-waitCh:
			if err == nil {
				return
			}
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			log.Fatalf("psql failed: %v", err)
		}
	}
}

func buildRawURL(connectionURLArg, host string, port int, user, dbName string) (string, bool, error) {
	if connectionURLArg != "" {
		if host != "" || user != "" || dbName != "" || port != 5432 {
			return "", false, fmt.Errorf("positional connection URL cannot be combined with -host, -port, -user, or -db")
		}
		parsedURL, err := url.Parse(connectionURLArg)
		if err != nil {
			return "", false, fmt.Errorf("failed to parse positional connection URL: %w", err)
		}
		switch parsedURL.Scheme {
		case "postgres+rds-iam":
			return connectionURLArg, true, nil
		case "postgres", "postgresql":
			return connectionURLArg, false, nil
		default:
			return "", false, fmt.Errorf("unsupported connection URL scheme %q (expected postgres, postgresql, or postgres+rds-iam)", parsedURL.Scheme)
		}
	}

	if host == "" || user == "" || dbName == "" {
		return "", false, fmt.Errorf("host, user, and db are required when no positional connection URL is provided")
	}
	if port <= 0 {
		return "", false, fmt.Errorf("invalid port: %d", port)
	}

	iamURL := &url.URL{
		Scheme: "postgres+rds-iam",
		User:   url.User(user),
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/" + dbName,
	}
	return iamURL.String(), true, nil
}

func printCallerIdentity(ctx context.Context, cfg aws.Config) error {
	stsClient := sts.NewFromConfig(cfg)

	out, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("STS GetCallerIdentity failed (creds invalid/expired or STS not allowed): %w", err)
	}

	fmt.Printf("Caller ARN:  %s\n", aws.ToString(out.Arn))
	return nil
}
