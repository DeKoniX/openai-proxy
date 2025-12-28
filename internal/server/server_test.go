package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dekonix/openai-proxy/internal/config"
	"github.com/dekonix/openai-proxy/internal/models"
	"github.com/dekonix/openai-proxy/internal/storage"
)

type fakeStore struct {
	proxies []models.Proxy
	logs    []models.APILog
	created []*models.Proxy
	updates []struct {
		id  int64
		upd storage.ProxyUpdate
	}
	resetIDs   []int64
	deletedIDs []int64

	createErr error
	updateErr error
	resetErr  error
	deleteErr error
}

func (f *fakeStore) cloneProxies() []models.Proxy {
	out := make([]models.Proxy, len(f.proxies))
	copy(out, f.proxies)
	return out
}

func (f *fakeStore) cloneLogs() []models.APILog {
	out := make([]models.APILog, len(f.logs))
	copy(out, f.logs)
	return out
}

func (f *fakeStore) ListProxies(ctx context.Context) ([]models.Proxy, error) {
	return f.cloneProxies(), nil
}

func (f *fakeStore) CreateProxy(ctx context.Context, proxy *models.Proxy) error {
	if f.createErr != nil {
		return f.createErr
	}
	if proxy.ID == 0 {
		proxy.ID = int64(len(f.proxies) + 1)
	}
	now := time.Now()
	if proxy.CreatedAt.IsZero() {
		proxy.CreatedAt = now
	}
	proxy.UpdatedAt = proxy.CreatedAt
	proxy.LastResetAt = proxy.CreatedAt

	cp := *proxy
	f.proxies = append(f.proxies, cp)
	f.created = append(f.created, &cp)
	return nil
}

func (f *fakeStore) UpdateProxy(ctx context.Context, id int64, upd storage.ProxyUpdate) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updates = append(f.updates, struct {
		id  int64
		upd storage.ProxyUpdate
	}{id: id, upd: upd})
	return nil
}

func (f *fakeStore) ResetProxyUsage(ctx context.Context, id int64) error {
	if f.resetErr != nil {
		return f.resetErr
	}
	f.resetIDs = append(f.resetIDs, id)
	return nil
}

func (f *fakeStore) DeleteProxy(ctx context.Context, id int64) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	filtered := f.proxies[:0]
	for _, p := range f.proxies {
		if p.ID != id {
			filtered = append(filtered, p)
		}
	}
	f.proxies = filtered
	f.deletedIDs = append(f.deletedIDs, id)
	return nil
}

func (f *fakeStore) SaveLog(ctx context.Context, entry *models.APILog) error {
	cp := *entry
	f.logs = append(f.logs, cp)
	return nil
}

func (f *fakeStore) ListRecent(ctx context.Context, limit int) ([]models.APILog, error) {
	logs := f.cloneLogs()
	if limit > 0 && len(logs) > limit {
		logs = logs[:limit]
	}
	return logs, nil
}

func (f *fakeStore) GetBody(ctx context.Context, id int64) (*models.APILog, error) {
	for _, log := range f.logs {
		if log.ID == id {
			cp := log
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (f *fakeStore) GetStats(ctx context.Context, period string, limit int) ([]models.StatPoint, error) {
	// Return empty stats for tests
	return []models.StatPoint{}, nil
}

func newTestServer(t *testing.T, store *fakeStore) *Server {
	t.Helper()
	cfg := &config.Config{ListenAddr: ":0", USDRUBRate: 90}
	srv, err := New(cfg, store)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	return srv
}

func TestListProxies(t *testing.T) {
	store := &fakeStore{
		proxies: []models.Proxy{
			{
				ID:                  1,
				PathPrefix:          "/openai",
				UpstreamURL:         "https://api.example.com/v1",
				TokenPriceInputUSD:  0.3,
				TokenPriceOutputUSD: 0.6,
				UsageInputTokens:    1000,
				UsageOutputTokens:   2000,
				UsageInputCostUSD:   0.3,
				UsageOutputCostUSD:  1.2,
				UsageInputCostRUB:   27,
				UsageOutputCostRUB:  108,
			},
		},
	}

	srv := newTestServer(t, store)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/proxies", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var resp struct {
		Proxies            []models.Proxy `json:"proxies"`
		TotalInputTokens   int64          `json:"total_input_tokens"`
		TotalOutputTokens  int64          `json:"total_output_tokens"`
		TotalInputCostUSD  float64        `json:"total_input_cost_usd"`
		TotalOutputCostUSD float64        `json:"total_output_cost_usd"`
		TotalInputCostRUB  float64        `json:"total_input_cost_rub"`
		TotalOutputCostRUB float64        `json:"total_output_cost_rub"`
	}

	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Proxies) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(resp.Proxies))
	}
	if resp.TotalInputTokens != 1000 || resp.TotalOutputTokens != 2000 {
		t.Fatalf("unexpected totals: %+v", resp)
	}
	if resp.TotalInputCostUSD != 0.3 || resp.TotalOutputCostUSD != 1.2 {
		t.Fatalf("unexpected usd totals: %+v", resp)
	}
	if resp.TotalInputCostRUB != 27 || resp.TotalOutputCostRUB != 108 {
		t.Fatalf("unexpected rub totals: %+v", resp)
	}
}

func TestCreateProxy(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(t, store)

	payload := map[string]interface{}{
		"name":                         "OpenAI",
		"path_prefix":                  "/openai",
		"upstream_url":                 "https://api.openai.com/v1",
		"upstream_key":                 "sk-xxx",
		"token_price_input_usd":        0.5,
		"token_price_cached_input_usd": 0.25,
		"token_price_output_usd":       1.1,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/admin/api/proxies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(store.created) != 1 {
		t.Fatalf("expected create to be called once, got %d", len(store.created))
	}
	created := store.created[0]
	if created.PathPrefix != "/openai" || created.UpstreamURL != "https://api.openai.com/v1" {
		t.Fatalf("unexpected created proxy: %+v", created)
	}
	if created.TokenPriceCachedInputUSD != 0.25 {
		t.Fatalf("unexpected cached input price: %+v", created)
	}
}

func TestDeleteProxy(t *testing.T) {
	store := &fakeStore{
		proxies: []models.Proxy{{ID: 1, PathPrefix: "/openai", UpstreamURL: "https://api.example.com"}},
	}
	srv := newTestServer(t, store)

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/proxies/1", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if len(store.deletedIDs) != 1 || store.deletedIDs[0] != 1 {
		t.Fatalf("delete not recorded: %+v", store.deletedIDs)
	}
}

func TestLogsAPI(t *testing.T) {
	store := &fakeStore{
		logs: []models.APILog{
			{
				ID:              1,
				CreatedAt:       time.Now(),
				ProxyID:         1,
				ProxyName:       "OpenAI",
				ProxyPathPrefix: "/openai",
				Method:          http.MethodPost,
				Path:            "/v1/chat",
				ResponseStatus:  200,
			},
		},
	}
	srv := newTestServer(t, store)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?limit=10", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var logs []models.APILog
	if err := json.Unmarshal(rr.Body.Bytes(), &logs); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	if len(logs) != 1 || logs[0].ID != 1 {
		t.Fatalf("unexpected logs: %+v", logs)
	}
}

func TestUpdateProxy(t *testing.T) {
	store := &fakeStore{
		proxies: []models.Proxy{{ID: 1, Name: "Old Name", UpstreamKey: "old-key"}},
	}
	srv := newTestServer(t, store)

	payload := map[string]interface{}{
		"name":                         "New Name",
		"upstream_key":                 "new-key",
		"token_price_input_usd":        0.1,
		"token_price_cached_input_usd": 0.02,
		"token_price_output_usd":       0.2,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/admin/api/proxies/1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(store.updates) != 1 || store.updates[0].id != 1 {
		t.Fatalf("update not recorded: %+v", store.updates)
	}
	upd := store.updates[0].upd
	if upd.Name == nil || *upd.Name != "New Name" {
		t.Fatalf("unexpected name update: %+v", upd)
	}
	if upd.UpstreamKey == nil || *upd.UpstreamKey != "new-key" {
		t.Fatalf("unexpected key update: %+v", upd)
	}
	if upd.TokenPriceInputUSD == nil || *upd.TokenPriceInputUSD != 0.1 {
		t.Fatalf("unexpected input price update: %+v", upd)
	}
	if upd.TokenPriceCachedInputUSD == nil || *upd.TokenPriceCachedInputUSD != 0.02 {
		t.Fatalf("unexpected cached input price update: %+v", upd)
	}
	if upd.TokenPriceOutputUSD == nil || *upd.TokenPriceOutputUSD != 0.2 {
		t.Fatalf("unexpected output price update: %+v", upd)
	}
}

func TestResetProxyUsage(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(t, store)

	req := httptest.NewRequest(http.MethodPost, "/admin/api/proxies/42/reset", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if len(store.resetIDs) != 1 || store.resetIDs[0] != 42 {
		t.Fatalf("reset not recorded: %+v", store.resetIDs)
	}
}

func TestStatsAPI(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(t, store)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats?period=day&limit=10", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var stats []models.StatPoint
	if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("expected empty stats, got %d", len(stats))
	}
}

func TestGetLogBody(t *testing.T) {
	store := &fakeStore{
		logs: []models.APILog{
			{
				ID:   1,
				Path: "/v1/test",
			},
		},
	}
	srv := newTestServer(t, store)

	req := httptest.NewRequest(http.MethodGet, "/admin/logs/1", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var log models.APILog
	if err := json.Unmarshal(rr.Body.Bytes(), &log); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	if log.ID != 1 || log.Path != "/v1/test" {
		t.Fatalf("unexpected log: %+v", log)
	}
}

func TestHelpers(t *testing.T) {
	if got := normalizePathPrefix("openai/"); got != "/openai" {
		t.Fatalf("normalizePathPrefix: expected /openai, got %s", got)
	}
	if got := countTokens([]byte("hello  world")); got != 2 {
		t.Fatalf("countTokens: expected 2, got %d", got)
	}

	srv := &Server{usdToRubRate: 100}
	usd, rub := srv.calculateCost(2.0, 1_000_000)
	if usd != 2.0 || rub != 200 {
		t.Fatalf("calculateCost: expected (2,200), got (%v,%v)", usd, rub)
	}

	usageBody := []byte(`{"usage":{"prompt_tokens":1200000,"completion_tokens":500000,"prompt_tokens_details":{"cached_tokens":200000}}}`)
	usage, ok := extractUsageFromBody(usageBody)
	if !ok {
		t.Fatalf("expected usage to be parsed")
	}
	if usage.InputTokens != 1200000 || usage.CachedInputTokens != 200000 || usage.OutputTokens != 500000 {
		t.Fatalf("unexpected usage parse: %+v", usage)
	}

	inputUSD, outputUSD, inputRUB, outputRUB := srv.calculateUsageCosts(usage, 2.0, 1.0, 3.0)
	if math.Abs(inputUSD-2.2) > 1e-9 || math.Abs(outputUSD-1.5) > 1e-9 {
		t.Fatalf("calculateUsageCosts usd mismatch: in=%v out=%v", inputUSD, outputUSD)
	}
	if math.Abs(inputRUB-220) > 1e-6 || math.Abs(outputRUB-150) > 1e-6 {
		t.Fatalf("calculateUsageCosts rub mismatch: in=%v out=%v", inputRUB, outputRUB)
	}
}
