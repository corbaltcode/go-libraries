package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

const (
	nessusUserName      = "nessus_scan_user"
	passwordLength      = 22
	passwordCharset     = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	defaultPostgresPort = "5432"
)

type dbConnInfo struct {
	Host   string
	Port   int
	DBName string
	User   string
	RawDSN string
}

type nessusSecret struct {
	Engine   string `json:"engine"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
}

func randomPassword(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("password length must be > 0")
	}

	max := big.NewInt(int64(len(passwordCharset)))
	out := make([]byte, n)

	for i := 0; i < n; i++ {
		x, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("rand.Int: %w", err)
		}
		out[i] = passwordCharset[x.Int64()]
	}

	return string(out), nil
}

func parseDSN(dsn string) (*dbConnInfo, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DSN as URL: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, fmt.Errorf("unexpected scheme %q in DSN, expected postgres or postgresql", u.Scheme)
	}

	user := ""
	if u.User != nil {
		user = u.User.Username()
	}

	host := u.Hostname()

	portStr := u.Port()
	if portStr == "" {
		portStr = defaultPostgresPort
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port %q: %w", portStr, err)
	}

	dbname := strings.TrimPrefix(u.Path, "/")

	// If path is empty, optionally look for ?dbname=foo
	if dbname == "" {
		if qName := u.Query().Get("dbname"); qName != "" {
			dbname = qName
		}
	}

	// If still empty, fall back to username (lib/pq's default behavior)
	if dbname == "" && user != "" {
		dbname = user
	}

	return &dbConnInfo{
		Host:   host,
		Port:   port,
		DBName: dbname,
		User:   user,
		RawDSN: dsn,
	}, nil
}

func loadAWSConfig(ctx context.Context) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx)
}

func getSecretIfExists(ctx context.Context, sm *secretsmanager.Client, name string) (*nessusSecret, error) {
	out, err := sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(name),
	})
	if err != nil {
		var rnfe *smtypes.ResourceNotFoundException
		if errors.As(err, &rnfe) {
			return nil, nil
		}
		return nil, fmt.Errorf("GetSecretValue failed: %w", err)
	}

	if out.SecretString == nil {
		return nil, fmt.Errorf("existing secret %q does not have SecretString", name)
	}

	var sec nessusSecret
	if err := json.Unmarshal([]byte(*out.SecretString), &sec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal existing secret JSON: %w", err)
	}
	return &sec, nil
}

func createSecret(ctx context.Context, sm *secretsmanager.Client, name string, sec *nessusSecret) error {
	payload, err := json.Marshal(sec)
	if err != nil {
		return fmt.Errorf("failed to marshal secret JSON: %w", err)
	}
	_, err = sm.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(name),
		SecretString: aws.String(string(payload)),
	})
	if err != nil {
		return fmt.Errorf("CreateSecret failed: %w", err)
	}
	return nil
}

func findDBInstanceByEndpoint(ctx context.Context, client *rds.Client, host string, port int32) (string, error) {
	var marker *string

	for {
		out, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
			Marker: marker,
		})
		if err != nil {
			return "", fmt.Errorf("DescribeDBInstances failed: %w", err)
		}

		for _, inst := range out.DBInstances {
			if inst.Endpoint == nil {
				continue
			}
			if aws.ToString(inst.Endpoint.Address) == host && aws.ToInt32(inst.Endpoint.Port) == port {
				return aws.ToString(inst.DBInstanceIdentifier), nil
			}
		}

		if out.Marker == nil || len(out.DBInstances) == 0 {
			break
		}
		marker = out.Marker
	}

	return "", fmt.Errorf("no RDS DB instance found with endpoint %s:%d", host, port)
}

func main() {
	log.SetFlags(0)

	if len(os.Args) != 2 {
		log.Fatalf("Usage: %s <postgres-dsn>", os.Args[0])
	}
	dsn := os.Args[1]

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	connInfo, err := parseDSN(dsn)
	if err != nil {
		log.Fatalf("Error parsing DSN: %v", err)
	}

	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		log.Fatalf("Error connecting to database: %s", err)
	}
	defer db.Close()

	awsCfg, err := loadAWSConfig(ctx)
	if err != nil {
		log.Fatalf("Failed to load AWS config: %v", err)
	}

	smClient := secretsmanager.NewFromConfig(awsCfg)
	rdsClient := rds.NewFromConfig(awsCfg)

	dbIdentifier, err := findDBInstanceByEndpoint(ctx, rdsClient, connInfo.Host, int32(connInfo.Port))
	if err != nil {
		log.Fatalf("Could not look up RDS DB instance for endpoint %s:%d: %v", connInfo.Host, connInfo.Port, err)
	}
	log.Printf("Discovered RDS DB instance identifier: %s", dbIdentifier)

	secretName := dbIdentifier + "_nessus"
	log.Printf("Using secret name: %s", secretName)

	// Secrets Manager is canonical for password:
	// - If it exists, reuse its password
	// - If it doesn't, generate & create it
	existingSecret, err := getSecretIfExists(ctx, smClient, secretName)
	if err != nil {
		log.Fatalf("Error checking existing secret: %v", err)
	}

	var password string
	if existingSecret != nil && existingSecret.Password != "" {
		password = existingSecret.Password
		log.Printf("Reusing existing password from secret %q.", secretName)
	} else {
		password, err = randomPassword(passwordLength)
		if err != nil {
			log.Fatalf("Error generating random password: %v", err)
		}
		log.Printf("Generated new password for %s.", nessusUserName)
	}

	secretBody := &nessusSecret{
		Engine:   "postgres",
		Host:     connInfo.Host,
		Port:     connInfo.Port,
		Username: nessusUserName,
		Password: password,
		DBName:   connInfo.DBName,
	}

	if existingSecret == nil {
		if err := createSecret(ctx, smClient, secretName, secretBody); err != nil {
			log.Fatalf("Error creating secret %q: %v", secretName, err)
		}
		log.Printf("Created secret %q in Secrets Manager.", secretName)
	}

	// 1) Ensure user exists (no password embedded)
	ensureUserSQL := fmt.Sprintf(`
DO $do$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = %s) THEN
    CREATE USER %s;
  END IF;
END
$do$;
`,
		pq.QuoteLiteral(nessusUserName),
		pq.QuoteIdentifier(nessusUserName),
	)

	if _, err := db.ExecContext(ctx, ensureUserSQL); err != nil {
		log.Fatalf("Failed to ensure %s exists: %v", nessusUserName, err)
	}

	// 2) Set password (ALTER ROLE does not support parameter placeholders)
	alterRoleSQL := fmt.Sprintf(
		"ALTER ROLE %s WITH PASSWORD %s",
		pq.QuoteIdentifier(nessusUserName),
		pq.QuoteLiteral(password),
	)

	if _, err := db.ExecContext(ctx, alterRoleSQL); err != nil {
		log.Fatalf("Failed to set password for %s: %v", nessusUserName, err)
	}

	// 3) Grants
	grantSQL := fmt.Sprintf(
		"GRANT pg_read_all_settings TO %s",
		pq.QuoteIdentifier(nessusUserName),
	)

	if _, err := db.ExecContext(ctx, grantSQL); err != nil {
		log.Fatalf("Failed to grant pg_read_all_settings to %s: %v", nessusUserName, err)
	}

	// CURRENT_USER is the user this script authenticated as on this connection.
	if _, err := db.ExecContext(ctx, "GRANT rds_iam TO CURRENT_USER"); err != nil {
		log.Fatalf("Failed to grant rds_iam to CURRENT_USER: %v", err)
	}
}
