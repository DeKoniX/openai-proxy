# AGENTS.md - internal/storage

**Scope:** PostgreSQL persistence, migrations, query operations

## OVERVIEW
PostgreSQL storage layer using pgx/v5. Handles connection pooling, schema migrations, and all database operations for logs and proxy configurations.

## WHERE TO LOOK
| Task | Function | Notes |
|------|----------|-------|
| Add table/column | runMigrations() | Append to stmts slice |
| Modify proxy schema | proxies table DDL | Lines ~252-265 in runMigrations |
| Modify logs schema | api_logs table DDL | Lines ~266-289 in runMigrations |
| New query method | Add to Store struct | Follow existing patterns |
| Check unique violations | IsUniqueViolation() | PostgreSQL 23505 error code |

## KEY PATTERNS

### Migrations
Migrations run automatically on Store initialization:
```go
stmts := []string{
    `CREATE TABLE IF NOT EXISTS...`,
    `ALTER TABLE...`,
    // Order matters - create before alter
}
```

Migrations are idempotent (IF NOT EXISTS, IF NOT EXISTS for ADD COLUMN).

### Query Construction
Dynamic updates use positional parameters:
```go
setClauses = append(setClauses, fmt.Sprintf("name = $%d", idx))
args = append(args, *upd.Name)
```

Always use parameterized queries - never string concatenation for values.

### Error Handling
- Wrap with fmt.Errorf: `fmt.Errorf("select api_logs: %w", err)`
- Check pgconn.PgError for specific codes
- ErrNoFieldsToUpdate for empty updates

### Context Timeouts
All public methods accept context:
```go
ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
defer cancel()
```

## TRANSACTIONS

Current implementation uses auto-commit per query. For multi-statement transactions:
```go
tx, err := s.pool.Begin(ctx)
// ... use tx instead of s.pool
err = tx.Commit(ctx)
```

## INDEXES

Currently created:
- idx_proxies_path_prefix - for route lookup
- idx_api_logs_created_at - for recent logs query

Add new indexes in runMigrations for query performance.

## ANTI-PATTERNS

- **DON'T** use * in SELECT - list columns explicitly (model field mapping)
- **DON'T** forget rows.Err() check after iteration
- **DON'T** defer Close() inside loops (use func scope)
- **DON'T** use fmt.Sprintf for WHERE conditions with user input (SQL injection)
