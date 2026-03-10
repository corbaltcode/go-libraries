# rds-iam-dsn

A CLI that resolves a `postgres+rds-iam://...` URL into a usable tokenized PostgreSQL DSN and prints it to stdout.

Use this when you want to script `psql`, `pg_dump`, or other Postgres tools without manual IAM token generation.

## Installation

```bash
go install github.com/corbaltcode/go-libraries/cmd/rds-iam-dsn@latest
```

Or build from source:

```bash
cd ./cmd/rds-iam-dsn
go build
```

## Prerequisites

- **AWS credentials** configured (env vars, `~/.aws/credentials`, IAM role, etc.)
- **AWS region** configured for SDK resolution (for example: `AWS_REGION`, shared config profile, or runtime role config)
- **RDS IAM authentication enabled** on your database instance
- A DB user configured for IAM auth (`CREATE USER myuser WITH LOGIN; GRANT rds_iam TO myuser;`)

## Usage

```bash
rds-iam-dsn '<postgres+rds-iam-url>'
```

- Database path is optional. If omitted, `pgutils` defaults DB name to the username.
- The command prints the resolved DSN to **stdout**.

## Examples

Resolve DSN only:

```bash
rds-iam-dsn 'postgres+rds-iam://app_user@mydb.abc123.us-east-1.rds.amazonaws.com:5432/myapp'
```

Use with `psql` in a script:

```bash
DSN="$(rds-iam-dsn 'postgres+rds-iam://app_user@mydb.abc123.us-east-1.rds.amazonaws.com:5432/myapp')"
psql "$DSN"
```

Or directly:

```bash
psql "$(rds-iam-dsn 'postgres+rds-iam://app_user@mydb.abc123.us-east-1.rds.amazonaws.com:5432/myapp')"
```

Use with `pg_dump`:

```bash
DSN="$(rds-iam-dsn 'postgres+rds-iam://app_user@mydb.abc123.us-east-1.rds.amazonaws.com:5432/myapp')"
pg_dump "$DSN" > myapp.sql
```

Cross-account role assumption:

```bash
rds-iam-dsn 'postgres+rds-iam://app_user@mydb.abc123.us-east-1.rds.amazonaws.com:5432/myapp?assume_role_arn=arn:aws:iam::123456789012:role/db-connect&assume_role_session_name=rds-iam-dsn'
```

## Troubleshooting

`PAM authentication failed for user "<user>"`

- This indicates IAM database authentication failed, but the message itself is not specific.
- Check RDS IAM auth error logs in CloudWatch:
  `/aws/rds/instance/<db-instance-identifier>/iam-db-auth-error`

`pg_hba.conf rejects connection for host "...", user "...", database "...", no encryption`

- This usually means the connection attempt was not encrypted.
- In MAC-FC, RDS parameter groups should enforce SSL. If this appears, verify the endpoint, user, and DSN being used.

## Notes

- IAM auth tokens are short-lived (typically 15 minutes). Generate DSNs close to use time.
- Treat emitted DSNs as secrets while valid.
