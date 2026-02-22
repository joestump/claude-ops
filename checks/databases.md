<!-- Governing: SPEC-0002 REQ-1 (markdown as sole instruction format), REQ-8 (no build step), REQ-9 (self-documenting) -->

# Database Health Checks

<!-- Governing: SPEC-0002 REQ-5 — Embedded Command Examples -->

## PostgreSQL

### How to Check

Use the Postgres MCP server or CLI. Connect to the database at the hostname defined in the inventory — never localhost unless the inventory explicitly says so.

```sql
-- Connection count
SELECT count(*) FROM pg_stat_activity;

-- Database sizes
SELECT datname, pg_size_pretty(pg_database_size(datname)) FROM pg_database WHERE datistemplate = false;

-- Dead tuple ratio (indicates need for vacuum)
SELECT relname, n_dead_tup, n_live_tup,
  CASE WHEN n_live_tup > 0 THEN round(n_dead_tup::numeric / n_live_tup, 4) ELSE 0 END as dead_ratio
FROM pg_stat_user_tables
WHERE n_dead_tup > 1000
ORDER BY n_dead_tup DESC LIMIT 10;

-- Long-running queries
SELECT pid, now() - pg_stat_activity.query_start AS duration, query
FROM pg_stat_activity
WHERE state != 'idle' AND now() - pg_stat_activity.query_start > interval '5 minutes';
```

### What's Healthy

- Connection count below max_connections (check with `SHOW max_connections`)
- Dead tuple ratio below 0.1 for most tables
- No queries running longer than 5 minutes (unless expected)

### Warning Signs

<!-- Governing: SPEC-0002 REQ-6 — Contextual Adaptation -->

- Connection count > 80% of max: approaching limit
- Dead tuple ratio > 0.2: autovacuum may be struggling. Some tables with high write throughput (e.g., session tables, event logs) may naturally have higher ratios — consider the table's purpose before flagging.
- Long-running queries: potential locks or runaway queries. Some services run intentional long queries (reporting, migrations) — check the query content before flagging.

## MariaDB / MySQL

### How to Check

```sql
-- Connection count
SHOW STATUS LIKE 'Threads_connected';

-- Process list
SHOW PROCESSLIST;

-- Table status for a database
SHOW TABLE STATUS FROM <database>;
```

Replace `<database>` with the actual database name from the service configuration.

### What's Healthy

- Threads_connected well below max_connections
- No long-running queries (unless expected)
- Tables not marked as crashed

## Redis

### How to Check

```
INFO memory
INFO clients
INFO stats
DBSIZE
```

### What's Healthy

- Memory usage below maxmemory (if set)
- Connected clients reasonable (not thousands)
- Evicted keys rate is low or zero
- No blocked clients

### Warning Signs

- `used_memory` approaching `maxmemory`: evictions will start
- `evicted_keys` increasing: cache pressure
- `blocked_clients` > 0: something is waiting on a blocking operation
