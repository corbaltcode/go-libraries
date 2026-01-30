package pgutils

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"

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

// ConnectionStringProvider returns a Postgres connection string for use by clients
// that need a DSN (e.g., pq.Listener) or to build a connector.
type ConnectionStringProvider interface {
	ConnectionString(ctx context.Context) (string, error)
}

// NewConnectionStringProviderFromURL constructs a ConnectionStringProvider from a URL-form DSN.
//
// Standard Postgres example:
//
//	postgres://user:pass@host:5432/dbname?sslmode=require
//
// IAM example 1:
//
//	postgres+rds-iam://user@host:5432/dbname
//
// IAM example 2 (cross-account):
//
//	postgres+rds-iam://user@host:5432/dbname?assume_role_arn=...&assume_role_session_name=...
//
// For postgres+rds-iam, the provider generates a fresh IAM auth token on each ConnectionString(ctx) call.
func NewConnectionStringProviderFromURL(ctx context.Context, rawURL string) (ConnectionStringProvider, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, fmt.Errorf("rawURL cannot be empty")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing URL: %w", err)
	}

	switch u.Scheme {
	case "postgres", "postgresql":
		return &staticConnectionStringProvider{connectionString: u.String()}, nil
	case "postgres+rds-iam":
		return newIAMConnectionStringProviderFromURL(ctx, u)
	default:
		return nil, fmt.Errorf("unsupported URL scheme: %s", u.Scheme)
	}
}

// NewConnectorFromURL constructs a driver.Connector from a URL-form DSN.
//
// Standard Postgres example:
//
//	postgres://user:pass@host:5432/dbname
//
// IAM example 1:
//
//	postgres+rds-iam://user@host:5432/dbname
//
// IAM example 2 (cross-account):
//
//	postgres+rds-iam://user@host:5432/dbname?assume_role_arn=...&assume_role_session_name=...
//
// For postgres+rds-iam, each Connect(ctx) call uses a fresh IAM auth token.
func NewConnectorFromURL(ctx context.Context, rawURL string) (driver.Connector, error) {
	provider, err := NewConnectionStringProviderFromURL(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return &postgresqlConnector{connectionStringProvider: provider}, nil
}

// AddSearchPathToURL returns a copy of u with search_path set in the query string.
// It returns an error if search_path is already present.
func AddSearchPathToURL(rawURL string, searchPath string) (string, error) {
	if strings.TrimSpace(rawURL) == "" {
		return "", fmt.Errorf("rawURL cannot be empty")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("error parsing URL: %w", err)
	}

	if u == nil {
		return "", fmt.Errorf("URL cannot be nil")
	}
	if searchPath == "" {
		uCopy := *u
		return uCopy.String(), nil
	}

	uCopy := *u
	q := uCopy.Query()
	if v := q.Get("search_path"); v != "" {
		return "", fmt.Errorf("search_path already set to %q", v)
	}
	q.Set("search_path", searchPath)
	uCopy.RawQuery = q.Encode()
	return uCopy.String(), nil
}

type postgresqlConnector struct {
	connectionStringProvider ConnectionStringProvider
}

func (c *postgresqlConnector) Connect(ctx context.Context) (driver.Conn, error) {
	dsn, err := c.connectionStringProvider.ConnectionString(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting connection string from provider: %w", err)
	}
	pqConnector, err := pq.NewConnector(dsn)
	if err != nil {
		return nil, fmt.Errorf("error creating pq connector: %w", err)
	}

	return pqConnector.Connect(ctx)
}

func (c *postgresqlConnector) Driver() driver.Driver {
	return &pq.Driver{}
}

// ConnectDB opens a connection using the connector and verifies it with a ping
func ConnectDB(conn driver.Connector) (*sqlx.DB, error) {
	sqlDB := sql.OpenDB(conn)
	db := sqlx.NewDb(sqlDB, "postgres")
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// MustConnectDB is like ConnectDB but panics on error
func MustConnectDB(conn driver.Connector) *sqlx.DB {
	db, err := ConnectDB(conn)
	if err != nil {
		panic(err)
	}
	return db
}

type staticConnectionStringProvider struct {
	connectionString string
}

func (p *staticConnectionStringProvider) ConnectionString(ctx context.Context) (string, error) {
	return p.connectionString, nil
}

type rdsIAMConnectionStringProvider struct {
	RDSEndpoint         string
	Region              string
	User                string
	Database            string
	CredentialsProvider aws.CredentialsProvider
}

func (p *rdsIAMConnectionStringProvider) ConnectionString(ctx context.Context) (string, error) {
	authToken, err := auth.BuildAuthToken(ctx, p.RDSEndpoint, p.Region, p.User, p.CredentialsProvider)
	if err != nil {
		return "", fmt.Errorf("error building auth token: %w", err)
	}
	log.Printf("Signing RDS IAM token for Endpoint: %s User: %s Database: %s", p.RDSEndpoint, p.User, p.Database)

	dsnURL := &url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(p.User, authToken),
		Host:   p.RDSEndpoint,
		Path:   "/" + p.Database,
	}

	return dsnURL.String(), nil
}

func newIAMConnectionStringProviderFromURL(ctx context.Context, u *url.URL) (ConnectionStringProvider, error) {
	user := ""
	if u.User != nil {
		user = u.User.Username()
		if _, hasPw := u.User.Password(); hasPw {
			return nil, fmt.Errorf("postgres+rds-iam URL must not include a password")
		}
	}
	if user == "" {
		return nil, fmt.Errorf("postgres+rds-iam URL missing username")
	}

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("postgres+rds-iam URL missing host")
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
	supportedParams := map[string]struct{}{
		"assume_role_arn":          {},
		"assume_role_session_name": {},
	}
	for k := range q {
		if _, ok := supportedParams[k]; !ok {
			return nil, fmt.Errorf("postgres+rds-iam URL has unsupported query parameter: %s", k)
		}
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	if awsCfg.Region == "" {
		return nil, fmt.Errorf("AWS region is not configured")
	}

	creds := awsCfg.Credentials
	assumeRoleARN := q.Get("assume_role_arn")
	if assumeRoleARN != "" {
		stsClient := sts.NewFromConfig(awsCfg)
		sessionName := q.Get("assume_role_session_name")
		if sessionName == "" {
			sessionName = "pgutils-rds-iam"
		}
		log.Printf("RDS IAM Assuming Role: %s with session name: %s for Host: %s User: %s Database: %s", assumeRoleARN, sessionName, host, user, dbName)
		assumeProvider := stscreds.NewAssumeRoleProvider(stsClient, assumeRoleARN, func(opts *stscreds.AssumeRoleOptions) {
			opts.RoleSessionName = sessionName
		})
		creds = aws.NewCredentialsCache(assumeProvider)
	}

	return &rdsIAMConnectionStringProvider{
		Region:              awsCfg.Region,
		RDSEndpoint:         net.JoinHostPort(host, port),
		User:                user,
		Database:            dbName,
		CredentialsProvider: creds,
	}, nil
}
