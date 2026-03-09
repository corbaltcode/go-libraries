package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/corbaltcode/go-libraries/pgutils"
)

const usageTemplate = `Usage:
  %[2]s [-debug-aws] 'postgres+rds-iam://myuser@mydb.abc123.us-east-1.rds.amazonaws.com:5432/mydb'

Notes:
  Flags must come before the DSN (standard Go flag parsing).
  Database path is optional. If omitted, the database name defaults to the username.

Flags:
%[1]s

Examples:
  %[2]s 'postgres+rds-iam://myuser@mydb.abc123.us-east-1.rds.amazonaws.com:5432/mydb'
  %[2]s -debug-aws 'postgres+rds-iam://myuser@mydb.abc123.us-east-1.rds.amazonaws.com:5432/mydb'
`

func main() {
	rawURL, debugAWS, err := parseCLIArgs(os.Args[1:], os.Args[0])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(os.Stdout, os.Args[0])
			return
		}
		fmt.Fprintf(os.Stderr, "%v\n\n", err)
		printUsage(os.Stderr, os.Args[0])
		os.Exit(2)
	}

	if err := validateRDSIAMURL(rawURL); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}

	ctx := context.Background()

	if debugAWS {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			log.Fatalf("failed to load AWS config: %v", err)
		}
		if err := printCallerIdentity(ctx, cfg); err != nil {
			log.Fatalf("AWS credentials check failed: %v", err)
		}
	}

	connectionStringProvider, err := pgutils.NewConnectionStringProviderFromURLString(ctx, rawURL)
	if err != nil {
		log.Fatalf("failed to create connection string provider: %v", err)
	}

	dsnWithToken, err := connectionStringProvider.ConnectionString(ctx)
	if err != nil {
		log.Fatalf("failed to get connection string from provider: %v", err)
	}

	cmd := exec.Command("psql", dsnWithToken)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Ignore SIGINT in the wrapper so interactive Ctrl-C can be handled by psql.
	// Forward SIGTERM to the child process.
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

func newFlagSet(bin string, output io.Writer) (fs *flag.FlagSet, debugAWSFlag *bool) {
	fs = flag.NewFlagSet(bin, flag.ContinueOnError)
	fs.SetOutput(output)

	return fs,
		fs.Bool("debug-aws", false, "Print AWS caller identity before connecting")
}

func printUsage(output io.Writer, bin string) {
	fs, _ := newFlagSet(bin, io.Discard)

	var defaults bytes.Buffer
	fs.SetOutput(&defaults)
	fs.PrintDefaults()

	fmt.Fprintf(output, usageTemplate, strings.TrimRight(defaults.String(), "\n"), bin)
}

func parseCLIArgs(args []string, bin string) (rawURL string, debugAWS bool, err error) {
	fs, debugAWSFlag := newFlagSet(bin, io.Discard)

	if err := fs.Parse(args); err != nil {
		return "", false, err
	}

	positionals := fs.Args()
	if len(positionals) != 1 {
		return "", false, fmt.Errorf("expected exactly one positional RDS IAM connection URL argument, got %d", len(positionals))
	}

	return positionals[0], *debugAWSFlag, nil
}

func validateRDSIAMURL(rawURL string) error {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("failed to parse positional connection URL: %w", err)
	}
	if parsedURL.Scheme != "postgres+rds-iam" {
		return fmt.Errorf("unsupported connection URL scheme %q (expected postgres+rds-iam)", parsedURL.Scheme)
	}
	if parsedURL.User == nil || strings.TrimSpace(parsedURL.User.Username()) == "" {
		return fmt.Errorf("connection URL must include a database username")
	}
	if _, ok := parsedURL.User.Password(); ok {
		return fmt.Errorf("connection URL must not include a password for postgres+rds-iam")
	}
	if strings.TrimSpace(parsedURL.Host) == "" {
		return fmt.Errorf("connection URL must include a database host")
	}

	return nil
}

func printCallerIdentity(ctx context.Context, cfg aws.Config) error {
	stsClient := sts.NewFromConfig(cfg)

	out, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("STS GetCallerIdentity failed (creds invalid/expired or STS not allowed): %w", err)
	}

	fmt.Fprintf(os.Stderr, "Caller ARN: %s\n", aws.ToString(out.Arn))
	return nil
}
