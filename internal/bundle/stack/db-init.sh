#!/bin/sh
# Embedded by plat5 CLI. Local only: fixed passwords, and Postgres is not published.
# One Postgres for the local stack, one database per plane: plat5 (gateway, identity,
# audit, control plane), auth (customer sign-in), and the operator module's operator
# (the operator audit log) and operator_auth (operator sign-in). Runs on every start
# and creates only what is missing.
set -eu

psql() { command psql -v ON_ERROR_STOP=1 -h postgres -U plat5 "$@"; }

for db in auth operator operator_auth; do
  psql -d plat5 -tc "SELECT 1 FROM pg_database WHERE datname = '$db'" | grep -q 1 ||
    psql -d plat5 -c "CREATE DATABASE $db"
done

# The operator audit log's roles and the schema its owner migrates, as operator's
# compose/postgres-init.sh makes them: the owner migrates, the writer serves and can't
# change or delete an event.
for role in audit_owner audit_writer; do
  psql -d plat5 -tc "SELECT 1 FROM pg_roles WHERE rolname = '$role'" | grep -q 1 ||
    psql -d plat5 -c "CREATE ROLE $role LOGIN PASSWORD '$role'"
done
psql -d operator -c "CREATE SCHEMA IF NOT EXISTS audit AUTHORIZATION audit_owner"
