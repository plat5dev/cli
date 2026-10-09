-- Embedded by plat5 CLI. Local only: fixed passwords, and the database is not published.
-- The staff audit log's owner and writer roles, and the schema the owner migrates
-- (operator docs/audit.md#roles). Postgres runs this once, on an empty data directory.
CREATE ROLE audit_owner LOGIN PASSWORD 'audit_owner';
CREATE ROLE audit_writer LOGIN PASSWORD 'audit_writer';
CREATE SCHEMA audit AUTHORIZATION audit_owner;
