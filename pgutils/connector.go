package pgutils

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strings"
	"time"

	"database/sql"
	"database/sql/driver"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

const defaultPostgresPort = "5432"

type baseConnectionStringProvider interface {
	getBaseConnectionString(ctx context.Context) (string, error)
}

type PostgresqlConnector struct {
	baseConnectionStringProvider
	searchPath string
}

func (conn *PostgresqlConnector) WithSearchPath(searchPath string) *PostgresqlConnector {
	return &PostgresqlConnector{
		baseConnectionStringProvider: conn.baseConnectionStringProvider,
		searchPath:                   searchPath,
	}
}

func (conn *PostgresqlConnector) Connect(ctx context.Context) (driver.Conn, error) {
	dsn, err := conn.GetConnectionString(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection string: %w", err)
	}
	pqConnector, err := pq.NewConnector(dsn)
	if err != nil {
		return nil, fmt.Errorf("create pq connector: %w", err)
	}

	return pqConnector.Connect(ctx)
}

func (conn *PostgresqlConnector) GetConnectionString(ctx context.Context) (string, error) {
	dsn, err := conn.getBaseConnectionString(ctx)
	if err != nil {
		return "", fmt.Errorf("get base connection string: %w", err)
	}
	if conn.searchPath == "" {
		return dsn, nil
	}

	// Add search path
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse DSN URL: %w", err)
	}
	q := u.Query()
	if v := q.Get("search_path"); v != "" {
		return "", fmt.Errorf("search_path already set to %q", v)
	}
	q.Set("search_path", conn.searchPath) // url.Values will percent-encode commas as needed
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *PostgresqlConnector) Driver() driver.Driver {
	return &pq.Driver{}
}

type staticConnectionStringProvider struct {
	connectionString string
}

func (p *staticConnectionStringProvider) getBaseConnectionString(ctx context.Context) (string, error) {
	return p.connectionString, nil
}

func NewPostgresqlConnectorFromConnectionString(connectionString string) *PostgresqlConnector {
	return &PostgresqlConnector{
		baseConnectionStringProvider: &staticConnectionStringProvider{connectionString},
	}
}

type IAMAuthConfig struct {
	RDSEndpoint string
	User        string
	Database    string

	// Optional: cross-account role assumption.
	// Set this to a role ARN in the RDS account (Account A) that has rds-db:connect.
	AssumeRoleARN string

	// Optional: if your trust policy requires an external ID.
	AssumeRoleExternalID string

	// Optional: override the default session name.
	AssumeRoleSessionName string

	// Optional: override STS assume role duration.
	// If zero, SDK default is used.
	AssumeRoleDuration time.Duration
}

type iamAuthConnectionStringProvider struct {
	IAMAuthConfig

	region string
	creds  aws.CredentialsProvider
}

func (p *iamAuthConnectionStringProvider) getBaseConnectionString(ctx context.Context) (string, error) {
	authToken, err := auth.BuildAuthToken(ctx, p.RDSEndpoint, p.region, p.User, p.creds)
	if err != nil {
		return "", fmt.Errorf("building auth token: %w", err)
	}
	log.Printf("Signing RDS IAM token for \n  Endpoint: %s \n  User: %s \n  Database: %s", p.RDSEndpoint, p.User, p.Database)

	dsnURL := &url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(p.User, authToken),
		Host:   p.RDSEndpoint,
		Path:   "/" + p.Database,
	}

	return dsnURL.String(), nil
}

func NewPostgresqlConnectorWithIAMAuth(ctx context.Context, cfg *IAMAuthConfig) (*PostgresqlConnector, error) {
	if cfg.RDSEndpoint == "" || cfg.User == "" || cfg.Database == "" {
		return nil, errors.New("RDS endpoint, user, and database are required")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	if awsCfg.Region == "" {
		return nil, errors.New("AWS region is not configured")
	}

	creds := awsCfg.Credentials

	// Cross-account support:
	// If AssumeRoleARN is set, assume a role in the RDS account (Account A)
	// using the ECS task role creds from Account B as the source credentials.
	if cfg.AssumeRoleARN != "" {
		log.Printf("RDS IAM Assuming Role: %s for \n  Endpoint: %s \n  User: %s \n  Database: %s", cfg.AssumeRoleARN, cfg.RDSEndpoint, cfg.User, cfg.Database)
		stsClient := sts.NewFromConfig(awsCfg)

		sessionName := cfg.AssumeRoleSessionName
		if sessionName == "" {
			sessionName = "pgutils-rds-iam"
		}

		assumeProvider := stscreds.NewAssumeRoleProvider(stsClient, cfg.AssumeRoleARN, func(assumeRoleOpts *stscreds.AssumeRoleOptions) {
			assumeRoleOpts.RoleSessionName = sessionName

			if cfg.AssumeRoleExternalID != "" {
				assumeRoleOpts.ExternalID = aws.String(cfg.AssumeRoleExternalID)
			}

			if cfg.AssumeRoleDuration != 0 {
				assumeRoleOpts.Duration = cfg.AssumeRoleDuration
			}
		})

		// Cache to avoid calling STS too frequently.
		creds = aws.NewCredentialsCache(assumeProvider)
	}

	return &PostgresqlConnector{
		baseConnectionStringProvider: &iamAuthConnectionStringProvider{
			IAMAuthConfig: *cfg,
			region:        awsCfg.Region,
			creds:         creds,
		},
	}, nil
}

// Provides missing sqlx.OpenDB
func OpenDB(conn *PostgresqlConnector) *sqlx.DB {
	sqlDB := sql.OpenDB(conn)
	return sqlx.NewDb(sqlDB, "postgres")
}

// ConnectDB opens a connection using the connector and verifies it with a ping
func ConnectDB(conn *PostgresqlConnector) (*sqlx.DB, error) {
	db := OpenDB(conn)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// MustConnectDB is like ConnectDB but panics on error
func MustConnectDB(conn *PostgresqlConnector) *sqlx.DB {
	db, err := ConnectDB(conn)
	if err != nil {
		panic(err)
	}
	return db
}

// NewPostgresqlConnectorFromDSN constructs a PostgresqlConnector from either a normal
// Postgres DSN/connection string or the custom postgres+rds-iam DSN used for RDS IAM auth.
//
// IAM example 1: postgres+rds-iam://<user>@<host>[:<port>]/<dbname>
//
// Optional query params (for cross-account IAM):
//   - assume_role_arn: role ARN to assume.
//   - assume_role_session_name: only used when assume_role_arn is set; defaults to "pgutils-rds-iam" if omitted.
//
// IAM example 2: postgres+rds-iam://<user>@<host>[:<port>]/<dbname>?assume_role_arn=...&assume_role_session_name=...
func NewPostgresqlConnectorFromDSN(ctx context.Context, dsn string) (*PostgresqlConnector, error) {
	if dsn == "" {
		return nil, errors.New("DSN cannot be empty")
	}

	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DSN: %w", err)
	}

	if u.Scheme != "postgres+rds-iam" { // Not our custom scheme: hand off to existing DSN handling.
		return NewPostgresqlConnectorFromConnectionString(dsn), nil
	}

	user := ""
	if u.User != nil {
		user = u.User.Username()
		if _, hasPw := u.User.Password(); hasPw {
			return nil, fmt.Errorf("postgres+rds-iam DSN must not include a password")
		}
	}
	if user == "" {
		return nil, fmt.Errorf("postgres+rds-iam DSN missing username")
	}

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("postgres+rds-iam DSN missing host")
	}

	port := u.Port()
	if port == "" {
		port = defaultPostgresPort
	}

	// Match libpq/psql defaulting: if dbname isn't specified, dbname defaults to username.
	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" {
		dbName = user
	}

	q := u.Query()
	supportedParams := []string{"assume_role_arn", "assume_role_session_name", "assume_role_external_id", "assume_role_duration"}
	for k := range q {
		if !slices.Contains(supportedParams, k) {
			return nil, fmt.Errorf("postgres+rds-iam DSN has unsupported query parameter: %s", k)
		}
	}

	cfg := &IAMAuthConfig{
		RDSEndpoint: host + ":" + port,
		User:        user,
		Database:    dbName,
	}

	assumeRoleARN := q.Get("assume_role_arn")
	if assumeRoleARN != "" {
		cfg.AssumeRoleARN = assumeRoleARN
		cfg.AssumeRoleSessionName = q.Get("assume_role_session_name")
	}

	return NewPostgresqlConnectorWithIAMAuth(ctx, cfg)
}
