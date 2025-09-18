package pgutils

import (
	"crypto/sha1"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Given a postgres connection string, generate a new one with the search path
// (https://www.postgresql.org/docs/17/ddl-schemas.html#DDL-SCHEMAS-PATH)
// set to just the given schema name.
func GetConnectionStringWithSearchPath(connStr, schemaName string) (string, error) {
	kvconn := connStr
	if strings.HasPrefix(connStr, "postgres://") || strings.HasPrefix(connStr, "postgresql://") {
		var err error
		kvconn, err = pq.ParseURL(connStr)
		if err != nil {
			return "", fmt.Errorf("Error parsing DB connection string: %w", err)
		}
	}
	kvconn = getCanonicalFormat(kvconn)
	filteredKVs := []string{}
	kvs := strings.Split(kvconn, " ")
	for _, kv := range kvs {
		pieces := strings.SplitN(kv, "=", 2)
		if pieces[0] == "search_path" {
			return "", fmt.Errorf("search_path already set to %q", pieces[1])
		}
		filteredKVs = append(filteredKVs, kv)
	}
	filteredKVs = append(filteredKVs, fmt.Sprintf("search_path=%s", schemaName))
	return strings.Join(filteredKVs, " "), nil
}

// Connect to the database specified by the connection string, with the search path
// (https://www.postgresql.org/docs/17/ddl-schemas.html#DDL-SCHEMAS-PATH)
// set to just the given schema name.
func ConnectWithSchema(dbConnectionString, schemaName string) (*sqlx.DB, error) {
	connStringWithSearchPath, err := GetConnectionStringWithSearchPath(dbConnectionString, schemaName)
	if err != nil {
		return nil, fmt.Errorf("Error getting updated connection string: %w", err)
	}

	// Connect to database and set search_path
	db, err := sqlx.Connect("postgres", connStringWithSearchPath)
	if err != nil {
		return nil, fmt.Errorf("Error connecting to db: %w", err)
	}
	q := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName)
	_, err = db.Exec(q)
	if err != nil {
		return nil, fmt.Errorf("Error ensuring schema %q: %w", schemaName, err)
	}

	return db, nil
}

func getSchemaName(testName string) string {
	name := strings.ToLower(strings.ReplaceAll(testName, " ", ""))
	if strings.HasPrefix(name, "_") {
		name = strings.Replace(name, "_", "", 1)
	}

	// [20]byte
	hashedName := sha1.Sum([]byte(name))
	return fmt.Sprintf("a%x", hashedName)[:31]
}

// This function connects to the database as specified in the connection string
// and creates a new schema with a name based on the test name. It returns a new
// database connection with that schema set as the search path.
// (https://www.postgresql.org/docs/17/ddl-schemas.html#DDL-SCHEMAS-PATH)
// It also registers a cleanup function (https://pkg.go.dev/testing#T.Cleanup)
// that will drop the schema after the test completes.
func ReconnectWithSchemaForTest(dbConnectionString string, t *testing.T) *sqlx.DB {
	// Create test schema and set search path
	schemaName := getSchemaName(t.Name())

	connStringWithSearchPath, err := GetConnectionStringWithSearchPath(dbConnectionString, schemaName)
	if err != nil {
		t.Fatalf("Error getting connection string: %s", err)
	}

	// Connect to database and set search_path
	db := sqlx.MustConnect("postgres", connStringWithSearchPath)
	q := fmt.Sprintf(`
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.schemata
        WHERE schema_name = '%s'
    ) THEN
        RAISE EXCEPTION 'Failed to create schema "%s". It already exists.';
    END IF;
END
$$;
`, schemaName, schemaName)
	_, err = db.Exec(q)
	if err != nil {
		t.Fatalf("Schema %s may not have been deleted after a previous test run. If it exists, delete it.", schemaName)
	}
	q = fmt.Sprintf("CREATE SCHEMA %s", schemaName)
	db.MustExec(q)

	t.Cleanup(func() {
		q := fmt.Sprintf("DROP SCHEMA %s CASCADE", schemaName)
		db.MustExec(q)

		err := db.Close()
		if err != nil {
			t.Fatalf("failed to close connection to postgres database: %s", err)
		}
	})

	return db
}

func getCanonicalFormat(s string) string {
	re := regexp.MustCompile(`\s+`)
	str := re.ReplaceAllString(s, " ")
	re = regexp.MustCompile(`\s*=\s*`)
	str = re.ReplaceAllString(str, "=")
	return str
}
