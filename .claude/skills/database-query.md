# Skill: Database Query (Read-Only)

<!-- Governing: SPEC-0023 REQ-10, REQ-11, REQ-12; ADR-0022 -->

## Purpose

Execute read-only database queries against PostgreSQL and MariaDB/MySQL instances defined in repo inventories. Use this skill to check database connectivity, verify schema state, inspect row counts, and diagnose database-related service issues.

This skill is strictly read-only. It MUST NOT execute any data-modifying statements.

## Tier Requirement

Tier 1 minimum. This skill performs only read-only queries. All tiers may execute it. For database recovery operations (connection fixes, replication repair), Tier 3 is required and handled separately in playbooks.

## Tool Discovery

This skill uses the following tools in preference order:

### For PostgreSQL
1. **MCP**: `mcp__postgres__query` — check if available in tool listing
2. **CLI**: `psql` — check with `which psql`

### For MariaDB/MySQL
1. **CLI**: `mysql` — check with `which mysql`

### Universal fallback
- **CLI**: `curl` to a database web UI (e.g., Adminer) if defined in inventory — unlikely but possible

## Execution

### PostgreSQL Queries

#### Using MCP: mcp__postgres__query

1. Call `mcp__postgres__query` with the SQL query string.
2. The MCP tool handles connection details from its configuration.
3. Log: `[skill:database-query] Using: mcp__postgres__query (MCP)`

#### Using CLI: psql

1. Construct the connection string from inventory variables:
   ```bash
   psql -h <host> -p <port> -U <user> -d <database> -c "<query>"
   ```
   - Host and credentials come from the repo inventory or environment variables.
   - Use `PGPASSWORD` environment variable or `.pgpass` file for authentication.
2. Log: `[skill:database-query] Using: psql (CLI)`
3. If MCP was preferred but unavailable, also log: `[skill:database-query] WARNING: PostgreSQL MCP not available, falling back to psql (CLI)`

### MariaDB/MySQL Queries

#### Using CLI: mysql

1. Construct the connection:
   ```bash
   mysql -h <host> -P <port> -u <user> -p"$DB_PASSWORD" <database> -e "<query>"
   ```
2. Log: `[skill:database-query] Using: mysql (CLI)`

### Common Diagnostic Queries

**Connection check (PostgreSQL)**:
```sql
SELECT 1 AS connection_ok;
```

**Connection check (MySQL)**:
```sql
SELECT 1 AS connection_ok;
```

**Active connections (PostgreSQL)**:
```sql
SELECT datname, count(*) FROM pg_stat_activity GROUP BY datname;
```

**Table sizes (PostgreSQL)**:
```sql
SELECT relname, pg_size_pretty(pg_total_relation_size(relid)) FROM pg_catalog.pg_statio_user_tables ORDER BY pg_total_relation_size(relid) DESC LIMIT 10;
```

**Replication status (PostgreSQL)**:
```sql
SELECT client_addr, state, sent_lsn, write_lsn, flush_lsn, replay_lsn FROM pg_stat_replication;
```

## Validation

After executing a query:
1. Confirm the query returned results (or an empty result set — which is valid).
2. If the query was a connection check, confirm the result is `1`.
3. Report results in a human-readable format.

If the query fails:
1. Report the error message (connection refused, authentication failed, timeout, etc.).
2. Do NOT retry automatically — report the failure for the monitoring cycle to handle.

## Scope Rules

This skill MUST NOT execute any of the following SQL operations:
- `INSERT`, `UPDATE`, `DELETE`, `UPSERT`, `MERGE`
- `DROP`, `CREATE`, `ALTER`, `TRUNCATE`
- `GRANT`, `REVOKE`
- `VACUUM FULL` (standard `VACUUM` is acceptable as read-only maintenance)
- Any statement that modifies data, schema, or permissions

If a query contains any denied keyword, the agent MUST:
1. Refuse the query.
2. Report: `[skill:database-query] SCOPE VIOLATION: Refusing to execute <operation> — read-only queries only`

## Dry-Run Behavior

When `CLAUDEOPS_DRY_RUN=true`:
- Read-only queries MAY still execute, as they do not modify state.
- The agent SHOULD log the query it would run for transparency.
- Log: `[skill:database-query] DRY RUN: Would execute query "<query>" on <host>:<port>/<database> using <tool>`
