# rds-iam-psql

A CLI that launches an interactive `psql` session from either:
- a positional connection URL, or
- individual `-host/-port/-user/-db` flags.

It supports standard PostgreSQL URLs and `pgutils` custom IAM URLs (`postgres+rds-iam://...`).

## Why?

RDS IAM authentication lets you connect using AWS credentials instead of a static DB password. IAM auth tokens are short-lived and inconvenient to generate manually. This tool resolves a fresh DSN through `pgutils` and opens `psql` for you.

## Installation

```bash
go install github.com/corbaltcode/go-libraries/cmd/rds-iam-psql@latest
```

Or build from source:

```bash
cd ./cmd/rds-iam-psql
go build
```

## Prerequisites

- **psql** installed and available in your PATH
- For IAM URLs (`postgres+rds-iam://...`), **AWS credentials** configured (env vars, `~/.aws/credentials`, IAM role, etc.)
- For IAM URLs (`postgres+rds-iam://...`), **AWS_REGION** set
- For IAM URLs (`postgres+rds-iam://...`), **RDS IAM authentication enabled** on your database instance
- For IAM URLs (`postgres+rds-iam://...`), a DB user configured for IAM auth (for example: `CREATE USER myuser WITH LOGIN; GRANT rds_iam TO myuser;`)

## Usage

```bash
rds-iam-psql [connection-url] [options]
```

```bash
rds-iam-psql -host <endpoint> -user <db-user> -db <database-name> [options]
```

`connection-url` supports:
- `postgres+rds-iam://user@host:5432/dbname`
- `postgres://user:pass@host:5432/dbname?...`
- `postgresql://user:pass@host:5432/dbname?...`

If `connection-url` is provided, do not combine it with `-host/-port/-user/-db`.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-host` | | Endpoint hostname (required if `connection-url` is not provided) |
| `-port` | `5432` | PostgreSQL port |
| `-user` | | DB username (required if `connection-url` is not provided) |
| `-db` | | DB name (required if `connection-url` is not provided) |
| `-psql` | `psql` | Path to the `psql` binary |
| `-sslmode` | `require` | SSL mode (`require`, `verify-full`, etc.) |
| `-search-path` | | PostgreSQL `search_path` to set on connection (e.g. `myschema,public`) |

## Examples

Positional IAM URL (your requested form):

```bash
./rds-iam-psql 'postgres+rds-iam://server@acremins-test.cicxifnkufnd.us-east-1.rds.amazonaws.com:5432/postgres'
```

IAM URL with cross-account role assumption:

```bash
rds-iam-psql 'postgres+rds-iam://app_user@mydb.abc123.us-east-1.rds.amazonaws.com:5432/myapp?assume_role_arn=arn:aws:iam::123456789012:role/db-connect&assume_role_session_name=rds-iam-psql'
```

Flag-based IAM connection:

```bash
rds-iam-psql -host mydb.abc123.us-east-1.rds.amazonaws.com -user app_user -db myapp
```

Standard PostgreSQL URL (non-IAM):

```bash
rds-iam-psql 'postgresql://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable'
```

With search path:

```bash
rds-iam-psql \
  -host mydb.abc123.us-east-1.rds.amazonaws.com \
  -user app_user \
  -db myapp \
  -search-path "app_schema,public"
```

## How It Works

1. Parses input from either positional URL or `-host/-port/-user/-db`.
2. Builds a `pgutils.ConnectionStringProvider` from the URL.
3. For IAM URLs, validates AWS auth context (including `AWS_REGION`).
4. Resolves a DSN from the provider and launches `psql` with:
- `PGPASSWORD` set from the URL password/token
- `PGSSLMODE` set from `-sslmode`
- `PGOPTIONS` set when `-search-path` is provided

## Setting Up IAM Auth on RDS

1. Enable IAM authentication on your RDS instance
2. Create a database user and grant IAM privileges:
   ```sql
   CREATE USER myuser WITH LOGIN;
   GRANT rds_iam TO myuser;
   ```
3. Attach an IAM policy allowing `rds-db:connect` to your AWS user/role:
   ```json
   {
     "Version": "2012-10-17",
     "Statement": [
       {
         "Effect": "Allow",
         "Action": "rds-db:connect",
         "Resource": "arn:aws:rds-db:<region>:<account-id>:dbuser:<dbi-resource-id>/<db-user>"
       }
     ]
   }
   ```
