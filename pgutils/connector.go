package pgutils

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"

	"database/sql"
	"database/sql/driver"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

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
	log.Printf("Signing RDS IAM token for user: %s", p.User)

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

	return &PostgresqlConnector{
		baseConnectionStringProvider: &iamAuthConnectionStringProvider{
			IAMAuthConfig: *cfg,
			region:        awsCfg.Region,
			creds:         awsCfg.Credentials,
		},
	}, nil
}

// Provides missing sqlx.OpenDB
func OpenDB(conn *PostgresqlConnector) *sqlx.DB {
	sqlDB := sql.OpenDB(conn)
	return sqlx.NewDb(sqlDB, "postgres")
}

