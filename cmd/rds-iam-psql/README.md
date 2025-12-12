# rds-iam-psql

A simple CLI tool that bridges AWS RDS IAM authentication into an interactive `psql` session. It generates a short-lived IAM auth token and launches `psql` with the token as the password, so you never have to manage database passwords.

## Why?

RDS IAM authentication lets you connect to PostgreSQL using your AWS credentials instead of a static database password. However, the auth tokens are temporary (15 minutes) and cumbersome to generate manually. This tool handles token generation automatically and drops you into a familiar `psql` shell.

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
- **AWS credentials** configured (via environment variables, `~/.aws/credentials`, IAM role, etc.)
- **RDS IAM authentication enabled** on your database instance
- A database user configured for IAM authentication (created with `CREATE USER myuser WITH LOGIN; GRANT rds_iam TO myuser;`)

## Usage

```bash
rds-iam-psql -host <rds-endpoint> -user <db-user> -db <database-name> [options]
```

### Required Flags

| Flag | Description |
|------|-------------|
| `-host` | RDS endpoint hostname (without port), e.g. `mydb.abc123.us-east-1.rds.amazonaws.com` |
| `-user` | Database username configured for IAM auth |
| `-db` | Database name to connect to |

### Optional Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `5432` | PostgreSQL port |
| `-region` | auto | AWS region. If omitted, inferred from AWS config or the hostname |
| `-profile` | | AWS shared config profile to use (e.g. `dev`, `prod`) |
| `-psql` | `psql` | Path to the `psql` binary |
| `-sslmode` | `require` | SSL mode (`require`, `verify-full`, etc.) |
| `-search-path` | | PostgreSQL `search_path` to set on connection (e.g. `myschema,public`) |

## Examples

Basic connection:

```bash
rds-iam-psql -host mydb.abc123.us-east-1.rds.amazonaws.com -user app_user -db myapp
```

With a specific AWS profile and schema:

```bash
rds-iam-psql \
  -host mydb.abc123.us-east-1.rds.amazonaws.com \
  -user app_user \
  -db myapp \
  -profile production \
  -search-path "app_schema,public"
```

Using a non-standard port and explicit region:

```bash
rds-iam-psql \
  -host mydb.abc123.us-east-1.rds.amazonaws.com \
  -port 5433 \
  -user admin \
  -db postgres \
  -region us-east-1
```

## How It Works

1. Loads your AWS credentials from the standard credential chain
2. Generates a temporary RDS IAM auth token using `auth.BuildAuthToken`
3. Launches `psql` with:
   - `PGPASSWORD` set to the auth token
   - `PGSSLMODE` set according to `-sslmode`
   - `PGOPTIONS` set if `-search-path` is provided
4. Attaches stdin/stdout/stderr for interactive use

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
