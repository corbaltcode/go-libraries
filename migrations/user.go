package migrations

import (
	"fmt"
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type PostgreSQLUser struct {
	Username   string
	GrantRoles []string
}

// Make sure that the given users exist in database cluster and have only the
// role memberships specified. If withPasswords is true, set each user's password
// to its username. Otherwise remove each user's password.
// All operations are done in a single transaction.
func EnsureUsersWithRoles(db *sqlx.DB, users []PostgreSQLUser, withPasswords bool) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("Error starting transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			err := tx.Rollback()
			if err != nil {
				log.Printf("Error rollink back: %s", err)
			}
		}
	}()

	for _, user := range users {
		createUserSQL := fmt.Sprintf(`
			DO $$
			DECLARE
				username text := %s;
			BEGIN
				IF NOT EXISTS (
					SELECT FROM pg_catalog.pg_user WHERE usename = username
				) THEN
					EXECUTE format('CREATE USER %%I', username);
				END IF;
			END
			$$`, pq.QuoteLiteral(user.Username))
		_, err := tx.Exec(createUserSQL)
		if err != nil {
			return fmt.Errorf("Failed to create user %q: %w", user.Username, err)
		}

		// Drop all existing roles
		dropRolesSQL := fmt.Sprintf(`
			DO $$
			DECLARE
				r RECORD;
			BEGIN
				FOR r IN
					SELECT roleid::regrole AS granted_role
					FROM pg_catalog.pg_auth_members
					WHERE member = %s::regrole
				LOOP
					EXECUTE format('REVOKE %%I FROM %s', r.granted_role);
				END LOOP;
			END
			$$;`, pq.QuoteLiteral(user.Username), pq.QuoteIdentifier(user.Username))
		_, err = tx.Exec(dropRolesSQL)
		if err != nil {
			return fmt.Errorf("Failed to drop roles for user %q: %w", user.Username, err)
		}

		// There could be privileges on a variety of different objects.
		// See https://www.postgresql.org/docs/current/sql-revoke.html
		// But we will just worry about roles.

		// Add roles
		for _, role := range user.GrantRoles {
			grantSQL := fmt.Sprintf("GRANT %s TO %s", pq.QuoteIdentifier(role), pq.QuoteIdentifier(user.Username))
			_, err = tx.Exec(grantSQL)
			if err != nil {
				return fmt.Errorf("Failed to give role %q to user %q: %w", role, user.Username, err)
			}
		}

		// Set or remove password
		if withPasswords {
			_, err = tx.Exec(
				fmt.Sprintf("ALTER USER %s WITH PASSWORD %s",
					pq.QuoteIdentifier(user.Username),
					pq.QuoteLiteral(user.Username)),
			)
			if err != nil {
				return fmt.Errorf("Failed to set password for user %q: %w", user.Username, err)
			}
		} else {
			_, err = tx.Exec(
				fmt.Sprintf("ALTER USER %s WITH PASSWORD NULL",
					pq.QuoteIdentifier(user.Username)),
			)
			if err != nil {
				return fmt.Errorf("Failed to remove password for user %q: %w", user.Username, err)
			}
		}
	}

	committed = true
	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("Error committing transaction: %w", err)
	}

	return nil
}
