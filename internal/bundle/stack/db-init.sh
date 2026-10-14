#!/bin/sh
# Embedded by plat5 CLI. Local only: fixed passwords, and Postgres is not published.
# One Postgres for the local stack, one database per plane: plat5 (gateway, identity,
# audit, control plane), auth (customer sign-in), staff_auth (staff sign-in), and
# operator (the staff audit log, with its owner and writer roles; operator
# docs/audit.md#roles). Runs on every start and creates only what is missing.
set -eu

psql() { command psql -v ON_ERROR_STOP=1 -h postgres -U plat5 "$@"; }

for db in auth staff_auth operator; do
  psql -d plat5 -tc "SELECT 1 FROM pg_database WHERE datname = '$db'" | grep -q 1 ||
    psql -d plat5 -c "CREATE DATABASE $db"
done

psql -d operator <<'SQL'
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'audit_owner') THEN
    CREATE ROLE audit_owner LOGIN PASSWORD 'audit_owner';
  END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'audit_writer') THEN
    CREATE ROLE audit_writer LOGIN PASSWORD 'audit_writer';
  END IF;
END
$$;
GRANT CONNECT ON DATABASE operator TO audit_owner, audit_writer;
CREATE SCHEMA IF NOT EXISTS audit AUTHORIZATION audit_owner;
SQL
