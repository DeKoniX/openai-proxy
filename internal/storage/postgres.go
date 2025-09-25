package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dekonix/openai-proxy/internal/models"
)

// Store wraps a pgx connection pool for saving and querying logs.
type Store struct {
	pool *pgxpool.Pool
}

// ErrNoFieldsToUpdate indicates an update call had no fields set.
var ErrNoFieldsToUpdate = errors.New("no fields to update")

// New creates a new Store backed by PostgreSQL.
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	if err := runMigrations(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	return &Store{pool: pool}, nil
}

// Close releases the underlying pool resources.
func (s *Store) Close() {
	s.pool.Close()
}

// SaveLog persists an API log entry.
func (s *Store) SaveLog(ctx context.Context, entry *models.APILog) error {
	const query = `
        INSERT INTO api_logs (
            proxy_id, method, path, query, request_headers, request_body,
            response_status, response_headers, response_body, error_message,
            request_tokens, response_tokens, total_tokens,
            cost_input_usd, cost_output_usd, cost_input_rub, cost_output_rub,
            cost_total_usd, cost_total_rub
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
        RETURNING id, created_at`

	err := s.pool.QueryRow(ctx, query,
		entry.ProxyID,
		entry.Method,
		entry.Path,
		entry.Query,
		entry.RequestHeaders,
		entry.RequestBody,
		entry.ResponseStatus,
		entry.ResponseHeaders,
		entry.ResponseBody,
		entry.ErrorMessage,
		entry.RequestTokens,
		entry.ResponseTokens,
		entry.TotalTokens,
		entry.CostInputUSD,
		entry.CostOutputUSD,
		entry.CostInputRUB,
		entry.CostOutputRUB,
		entry.CostTotalUSD,
		entry.CostTotalRUB,
	).Scan(&entry.ID, &entry.CreatedAt)

	if err != nil {
		return fmt.Errorf("insert api_log: %w", err)
	}
	return nil
}

// ListRecent returns the most recent logs limited by count.
func (s *Store) ListRecent(ctx context.Context, limit int) ([]models.APILog, error) {
	if limit <= 0 {
		limit = 50
	}

	const query = `
        SELECT l.id, l.created_at, COALESCE(l.proxy_id, 0), COALESCE(p.name, ''),
               COALESCE(p.path_prefix, ''), l.method, l.path, l.query, l.request_headers,
               l.response_status, l.response_headers, l.error_message,
               l.request_tokens, l.response_tokens, l.total_tokens,
               l.cost_input_usd, l.cost_output_usd, l.cost_input_rub, l.cost_output_rub,
               l.cost_total_usd, l.cost_total_rub
        FROM api_logs AS l
        LEFT JOIN proxies AS p ON p.id = l.proxy_id
        ORDER BY l.created_at DESC
        LIMIT $1`

	rows, err := s.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("select api_logs: %w", err)
	}
	defer rows.Close()

	var logs []models.APILog
	for rows.Next() {
		var entry models.APILog
		if err := rows.Scan(
			&entry.ID,
			&entry.CreatedAt,
			&entry.ProxyID,
			&entry.ProxyName,
			&entry.ProxyPathPrefix,
			&entry.Method,
			&entry.Path,
			&entry.Query,
			&entry.RequestHeaders,
			&entry.ResponseStatus,
			&entry.ResponseHeaders,
			&entry.ErrorMessage,
			&entry.RequestTokens,
			&entry.ResponseTokens,
			&entry.TotalTokens,
			&entry.CostInputUSD,
			&entry.CostOutputUSD,
			&entry.CostInputRUB,
			&entry.CostOutputRUB,
			&entry.CostTotalUSD,
			&entry.CostTotalRUB,
		); err != nil {
			return nil, fmt.Errorf("scan api_log: %w", err)
		}
		logs = append(logs, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate api_logs: %w", err)
	}

	return logs, nil
}

// GetBody fetches request and response bodies for a log entry.
func (s *Store) GetBody(ctx context.Context, id int64) (*models.APILog, error) {
	const query = `
        SELECT l.id, l.created_at, COALESCE(l.proxy_id, 0), COALESCE(p.name, ''),
               COALESCE(p.path_prefix, ''), l.method, l.path, l.query, l.request_headers, l.request_body,
               l.response_status, l.response_headers, l.response_body, l.error_message,
               l.request_tokens, l.response_tokens, l.total_tokens,
               l.cost_input_usd, l.cost_output_usd, l.cost_input_rub, l.cost_output_rub,
               l.cost_total_usd, l.cost_total_rub
        FROM api_logs AS l
        LEFT JOIN proxies AS p ON p.id = l.proxy_id
        WHERE l.id = $1`

	var entry models.APILog
	if err := s.pool.QueryRow(ctx, query, id).Scan(
		&entry.ID,
		&entry.CreatedAt,
		&entry.ProxyID,
		&entry.ProxyName,
		&entry.ProxyPathPrefix,
		&entry.Method,
		&entry.Path,
		&entry.Query,
		&entry.RequestHeaders,
		&entry.RequestBody,
		&entry.ResponseStatus,
		&entry.ResponseHeaders,
		&entry.ResponseBody,
		&entry.ErrorMessage,
		&entry.RequestTokens,
		&entry.ResponseTokens,
		&entry.TotalTokens,
		&entry.CostInputUSD,
		&entry.CostOutputUSD,
		&entry.CostInputRUB,
		&entry.CostOutputRUB,
		&entry.CostTotalUSD,
		&entry.CostTotalRUB,
	); err != nil {
		return nil, fmt.Errorf("select api_log: %w", err)
	}

	return &entry, nil
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS proxies (
	        id BIGSERIAL PRIMARY KEY,
	        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	        updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        name TEXT NOT NULL DEFAULT '',
        path_prefix TEXT NOT NULL UNIQUE,
        upstream_url TEXT NOT NULL,
        upstream_key TEXT NOT NULL DEFAULT '',
        token_price_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
        token_price_input_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
        token_price_output_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
        last_reset_at TIMESTAMPTZ NOT NULL DEFAULT to_timestamp(0)
	    );`,
		`CREATE TABLE IF NOT EXISTS api_logs (
	        id BIGSERIAL PRIMARY KEY,
	        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	        proxy_id BIGINT REFERENCES proxies(id),
	        method TEXT NOT NULL,
	        path TEXT NOT NULL,
	        query TEXT NOT NULL,
	        request_headers TEXT NOT NULL,
	        request_body BYTEA,
	        response_status INT NOT NULL,
	        response_headers TEXT NOT NULL,
	        response_body BYTEA,
	        error_message TEXT NOT NULL DEFAULT '',
	        request_tokens INT NOT NULL DEFAULT 0,
	        response_tokens INT NOT NULL DEFAULT 0,
	        total_tokens INT NOT NULL DEFAULT 0,
        cost_input_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
        cost_output_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
        cost_input_rub DOUBLE PRECISION NOT NULL DEFAULT 0,
        cost_output_rub DOUBLE PRECISION NOT NULL DEFAULT 0,
        cost_total_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
        cost_total_rub DOUBLE PRECISION NOT NULL DEFAULT 0
	    );`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS proxy_id BIGINT REFERENCES proxies(id);`,
		`ALTER TABLE api_logs ALTER COLUMN proxy_id DROP DEFAULT;`,
		`ALTER TABLE proxies ADD COLUMN IF NOT EXISTS token_price_usd DOUBLE PRECISION NOT NULL DEFAULT 0;`,
		`ALTER TABLE proxies ADD COLUMN IF NOT EXISTS token_price_input_usd DOUBLE PRECISION NOT NULL DEFAULT 0;`,
		`ALTER TABLE proxies ADD COLUMN IF NOT EXISTS token_price_output_usd DOUBLE PRECISION NOT NULL DEFAULT 0;`,
		`ALTER TABLE proxies ADD COLUMN IF NOT EXISTS last_reset_at TIMESTAMPTZ NOT NULL DEFAULT to_timestamp(0);`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS request_tokens INT NOT NULL DEFAULT 0;`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS response_tokens INT NOT NULL DEFAULT 0;`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS total_tokens INT NOT NULL DEFAULT 0;`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS cost_input_usd DOUBLE PRECISION NOT NULL DEFAULT 0;`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS cost_output_usd DOUBLE PRECISION NOT NULL DEFAULT 0;`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS cost_input_rub DOUBLE PRECISION NOT NULL DEFAULT 0;`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS cost_output_rub DOUBLE PRECISION NOT NULL DEFAULT 0;`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS cost_total_usd DOUBLE PRECISION NOT NULL DEFAULT 0;`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS cost_total_rub DOUBLE PRECISION NOT NULL DEFAULT 0;`,
		`UPDATE proxies SET
            token_price_input_usd = CASE WHEN token_price_input_usd = 0 THEN token_price_usd ELSE token_price_input_usd END,
            token_price_output_usd = CASE WHEN token_price_output_usd = 0 THEN token_price_usd ELSE token_price_output_usd END;`,
	}

	for _, stmt := range stmts {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	if _, err := pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_proxies_path_prefix ON proxies(path_prefix);`); err != nil {
		return fmt.Errorf("migrate proxies index: %w", err)
	}

	if _, err := pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_api_logs_created_at ON api_logs(created_at DESC);`); err != nil {
		return fmt.Errorf("migrate api_logs: %w", err)
	}
	return nil
}

// ListProxies returns configured proxy routes ordered by prefix length desc.
func (s *Store) ListProxies(ctx context.Context) ([]models.Proxy, error) {
	const query = `
        SELECT p.id, p.created_at, p.updated_at, p.name, p.path_prefix, p.upstream_url, p.upstream_key,
               p.token_price_input_usd, p.token_price_output_usd, p.last_reset_at,
               COALESCE(SUM(l.request_tokens), 0) AS usage_input_tokens,
               COALESCE(SUM(l.response_tokens), 0) AS usage_output_tokens,
               COALESCE(SUM(l.cost_input_usd), 0) AS usage_input_cost_usd,
               COALESCE(SUM(l.cost_output_usd), 0) AS usage_output_cost_usd,
               COALESCE(SUM(l.cost_input_rub), 0) AS usage_input_cost_rub,
               COALESCE(SUM(l.cost_output_rub), 0) AS usage_output_cost_rub
        FROM proxies AS p
        LEFT JOIN api_logs AS l
            ON l.proxy_id = p.id AND l.created_at >= p.last_reset_at
        GROUP BY p.id
        ORDER BY LENGTH(p.path_prefix) DESC, p.id ASC`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select proxies: %w", err)
	}
	defer rows.Close()

	var proxies []models.Proxy
	for rows.Next() {
		var p models.Proxy
		if err := rows.Scan(
			&p.ID,
			&p.CreatedAt,
			&p.UpdatedAt,
			&p.Name,
			&p.PathPrefix,
			&p.UpstreamURL,
			&p.UpstreamKey,
			&p.TokenPriceInputUSD,
			&p.TokenPriceOutputUSD,
			&p.LastResetAt,
			&p.UsageInputTokens,
			&p.UsageOutputTokens,
			&p.UsageInputCostUSD,
			&p.UsageOutputCostUSD,
			&p.UsageInputCostRUB,
			&p.UsageOutputCostRUB,
		); err != nil {
			return nil, fmt.Errorf("scan proxy: %w", err)
		}
		proxies = append(proxies, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxies: %w", err)
	}
	return proxies, nil
}

// CreateProxy inserts a new proxy route definition.
func (s *Store) CreateProxy(ctx context.Context, proxy *models.Proxy) error {
	const query = `
        INSERT INTO proxies (name, path_prefix, upstream_url, upstream_key, token_price_input_usd, token_price_output_usd, created_at, updated_at, last_reset_at)
        VALUES ($1, $2, $3, $4, $5, $6, now(), now(), now())
        RETURNING id, created_at, updated_at, last_reset_at`

	if err := s.pool.QueryRow(ctx, query,
		proxy.Name,
		proxy.PathPrefix,
		proxy.UpstreamURL,
		proxy.UpstreamKey,
		proxy.TokenPriceInputUSD,
		proxy.TokenPriceOutputUSD,
	).Scan(&proxy.ID, &proxy.CreatedAt, &proxy.UpdatedAt, &proxy.LastResetAt); err != nil {
		return fmt.Errorf("insert proxy: %w", err)
	}
	return nil
}

// UpdateProxy updates selected fields for a proxy row.
func (s *Store) UpdateProxy(ctx context.Context, id int64, upd ProxyUpdate) error {
	setClauses := make([]string, 0, 4)
	args := make([]interface{}, 0, 5)
	idx := 1

	if upd.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", idx))
		args = append(args, *upd.Name)
		idx++
	}
	if upd.TokenPriceInputUSD != nil {
		setClauses = append(setClauses, fmt.Sprintf("token_price_input_usd = $%d", idx))
		args = append(args, *upd.TokenPriceInputUSD)
		idx++
	}
	if upd.TokenPriceOutputUSD != nil {
		setClauses = append(setClauses, fmt.Sprintf("token_price_output_usd = $%d", idx))
		args = append(args, *upd.TokenPriceOutputUSD)
		idx++
	}
	if upd.UpstreamKey != nil {
		setClauses = append(setClauses, fmt.Sprintf("upstream_key = $%d", idx))
		args = append(args, *upd.UpstreamKey)
		idx++
	}
	if len(setClauses) == 0 {
		return ErrNoFieldsToUpdate
	}
	setClauses = append(setClauses, "updated_at = now()")

	query := fmt.Sprintf("UPDATE proxies SET %s WHERE id = $%d", strings.Join(setClauses, ", "), idx)
	args = append(args, id)

	if _, err := s.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("update proxy: %w", err)
	}
	return nil
}

// ResetProxyUsage sets last_reset_at to now for a proxy.
func (s *Store) ResetProxyUsage(ctx context.Context, id int64) error {
	if _, err := s.pool.Exec(ctx, `UPDATE proxies SET last_reset_at = now(), updated_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("reset proxy usage: %w", err)
	}
	return nil
}

// DeleteProxy removes a proxy and associated logs.
func (s *Store) DeleteProxy(ctx context.Context, id int64) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM api_logs WHERE proxy_id = $1`, id); err != nil {
		return fmt.Errorf("delete proxy logs: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM proxies WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete proxy: %w", err)
	}
	return nil
}

// ProxyUpdate describes optional fields for proxy update.
type ProxyUpdate struct {
	Name                *string
	TokenPriceInputUSD  *float64
	TokenPriceOutputUSD *float64
	UpstreamKey         *string
}

// IsUniqueViolation reports if error is unique constraint violation.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
