# RDS Database Initialization Tool

A utility for initializing and configuring RDS PostgreSQL databases. This tool manages the `nessus_scan_user` account for security scanning and enables RDS IAM authentication for the admin user.

## Overview

This tool performs two main tasks:

1. **Nessus scan user management** — Creates and maintains a dedicated database user (`nessus_scan_user`) for Nessus security scans, with credentials stored in AWS Secrets Manager.

2. **RDS IAM authentication setup** — Grants the `rds_iam` role to the admin user, enabling IAM-based database authentication for future connections.

## How It Works

### Nessus User Setup

The tool follows this workflow for the `nessus_scan_user`:

1. Parses the provided PostgreSQL DSN to extract connection details
2. Looks up the RDS instance identifier by matching the endpoint host and port
3. Checks for an existing secret named `{db-identifier}_nessus` in Secrets Manager
4. If no secret exists, generates a random 22-character password and creates the secret
5. If a secret exists, reuses the stored password
6. Creates the `nessus_scan_user` role in PostgreSQL if it doesn't exist
7. Sets (or updates) the user's password to match the Secrets Manager value
8. Grants `pg_read_all_settings` to the user for Nessus compliance scanning

### RDS IAM Setup

After configuring the Nessus user, the tool grants `rds_iam` to the currently authenticated user (the admin user specified in the DSN).

## Usage

```bash
./rds-init <postgres-dsn>
```

### Example

```bash
./rds-init "postgres://admin_user:password@mydb.abc123.us-east-1.rds.amazonaws.com:5432/myappdb"
```

## When to Run

- **Initial provisioning** — Run after creating a new RDS instance
- **Password rotation** — Run after manually updating the password in the `{db-identifier}_nessus` Secrets Manager secret to sync it to the database
- **Safe to rerun on failure** — On success password auth will be disabled for the admin user. As this tool requires password auth, subsequent runs will not be possible. If the script exits unsuccessfully before disabling password auth, it is safe to execute multiple times.

## Prerequisites

- AWS credentials configured with permissions for:
  - `secretsmanager:GetSecretValue`
  - `secretsmanager:CreateSecret`
  - `rds:DescribeDBInstances`
- Network access to the target RDS instance
- A PostgreSQL admin user with privileges to create roles and grant permissions

## Secrets Manager Secret

Secret names follow the pattern: `{rds-instance-identifier}_nessus`

## Notes

This tool is safe to run multiple times (e.g., if the admin user's IAM auth mode was reset). However, after the initial run, password authentication is typically disabled for the admin user. Subsequent executions using the same password-based DSN will fail.