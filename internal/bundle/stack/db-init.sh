#!/bin/sh
# Embedded by plat5 CLI. Local only: fixed passwords, and Postgres is not published.
# One Postgres for the local stack, one database per plane: plat5 (gateway, identity,
# audit, control plane) and auth (customer sign-in). Runs on every start and creates
# only what is missing.
set -eu

psql() { command psql -v ON_ERROR_STOP=1 -h postgres -U plat5 "$@"; }

for db in auth; do
  psql -d plat5 -tc "SELECT 1 FROM pg_database WHERE datname = '$db'" | grep -q 1 ||
    psql -d plat5 -c "CREATE DATABASE $db"
done
