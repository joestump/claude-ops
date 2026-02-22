<!-- Governing: SPEC-0002 REQ-1 (markdown as sole instruction format), REQ-2 (Check Document Structure), REQ-5 (Embedded Command Examples), REQ-8 (no build step), REQ-9 (self-documenting) -->

# Database Health Checks

## When to Run

For every service that has a database dependency (PostgreSQL, MariaDB/MySQL, Redis) defined in the repo inventory. Run these checks when the inventory includes database connection details (host, port, credentials) or when a database MCP server is configured.

## How to Check

Use the Postgres MCP server or CLI. Connect to the database at the hostname defined in the inventory — never localhost unless the inventory explicitly says so.

### PostgreSQL

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

### MariaDB / MySQL

Replace `<database>` with the actual database name from the service configuration.

```sql
-- Connection count
SHOW STATUS LIKE 'Threads_connected';

-- Process list
SHOW PROCESSLIST;

-- Table status for a database
SHOW TABLE STATUS FROM <database>;
```

### Redis

```
INFO memory
INFO clients
INFO stats
DBSIZE
```

## What's Healthy

### PostgreSQL
- Connection count below max_connections (check with `SHOW max_connections`)
- Dead tuple ratio below 0.1 for most tables
- No queries running longer than 5 minutes (unless expected)

### MariaDB / MySQL
- Threads_connected well below max_connections
- No long-running queries (unless expected)
- Tables not marked as crashed

### Redis
- Memory usage below maxmemory (if set)
- Connected clients reasonable (not thousands)
- Evicted keys rate is low or zero
- No blocked clients

### Warning Signs

<!-- Governing: SPEC-0002 REQ-6 — Contextual Adaptation -->

- **PostgreSQL**: Connection count > 80% of max (approaching limit), dead tuple ratio > 0.2 (autovacuum may be struggling — some tables with high write throughput may naturally have higher ratios), long-running queries (potential locks or runaway queries — some services run intentional long queries for reporting/migrations)
- **MariaDB/MySQL**: Long-running queries, crashed tables
- **Redis**: `used_memory` approaching `maxmemory` (evictions will start), `evicted_keys` increasing (cache pressure), `blocked_clients` > 0 (something is waiting on a blocking operation)

## What to Record

For each database checked:
- Database type (PostgreSQL, MariaDB, Redis)
- Host and port
- Connection count vs. max
- Database sizes (PostgreSQL/MariaDB)
- Dead tuple ratio for top tables (PostgreSQL)
- Memory usage vs. maxmemory (Redis)
- Number of long-running queries
- Whether it's healthy/degraded/down

## Special Cases

- Databases behind connection poolers (e.g., PgBouncer) may show a lower connection count than expected — check the pooler's stats as well if available
- Read replicas may have replication lag — note the lag but don't flag as unhealthy unless it exceeds a significant threshold (e.g., > 60 seconds)
- Redis instances used purely as caches (no persistence) may have high eviction rates by design — check the eviction policy before flagging
- Maintenance windows (e.g., autovacuum running, OPTIMIZE TABLE) may cause temporary spikes in resource usage — note but don't flag as unhealthy
