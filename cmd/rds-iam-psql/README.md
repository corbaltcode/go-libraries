# rds-iam-psql

A CLI that launches an interactive `psql` session from a required RDS IAM URL:
- positional `postgres+rds-iam://...` DSN
- optional `-search-path` flag
- optional `-debug-aws` flag

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
- **AWS credentials** configured (env vars, `~/.aws/credentials`, IAM role, etc.)
- **AWS region** configured for SDK resolution (for example: `AWS_REGION`, shared config profile, or runtime role config)
- **RDS IAM authentication enabled** on your database instance
- A DB user configured for IAM auth (for example: `CREATE USER myuser WITH LOGIN; GRANT rds_iam TO myuser;`)

## Usage

```bash
rds-iam-psql [-search-path "schema,public"] [-debug-aws] '<postgres+rds-iam-url>'
```

- Flags must come before the DSN (standard Go flag parsing behavior).
- `<postgres+rds-iam-url>` may omit the database path. When omitted, `pgutils` defaults the database name to the username.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-search-path` | | PostgreSQL `search_path` to set on connection (e.g. `myschema,public`) |
| `-debug-aws` | `false` | Print STS caller identity before connecting |

## Examples

Basic IAM URL:

```bash
./rds-iam-psql 'postgres+rds-iam://server@acremins-test.cicxifnkufnd.us-east-1.rds.amazonaws.com:5432/postgres'
```

IAM URL with cross-account role assumption:

```bash
rds-iam-psql 'postgres+rds-iam://app_user@mydb.abc123.us-east-1.rds.amazonaws.com:5432/myapp?assume_role_arn=arn:aws:iam::123456789012:role/db-connect&assume_role_session_name=rds-iam-psql'
```

With search path:

```bash
rds-iam-psql \
  -search-path "app_schema,public" \
  'postgres+rds-iam://app_user@mydb.abc123.us-east-1.rds.amazonaws.com:5432/myapp'
```

With AWS identity debugging:

```bash
rds-iam-psql -debug-aws 'postgres+rds-iam://app_user@mydb.abc123.us-east-1.rds.amazonaws.com:5432/myapp'
```

Without explicit database name (defaults to username):

```bash
rds-iam-psql 'postgres+rds-iam://app_user@mydb.abc123.us-east-1.rds.amazonaws.com:5432'
```

## How It Works

1. Parses and validates the positional IAM URL.
2. Builds a `pgutils` connection string provider from the IAM URL.
3. If `-search-path` is set, adds libpq `options=-csearch_path=...` to the connection URI before launching `psql`.
4. If `-debug-aws` is set, runs STS `GetCallerIdentity` and prints the caller ARN.
5. Resolves an IAM tokenized DSN from the provider and launches `psql` with:
- `PGPASSWORD` set from the generated token

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
