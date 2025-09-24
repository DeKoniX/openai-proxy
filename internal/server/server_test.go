package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
		"name":                   "OpenAI",
		"path_prefix":            "/openai",
		"upstream_url":           "https://api.openai.com/v1",
		"upstream_key":           "sk-xxx",
		"token_price_input_usd":  0.5,
		"token_price_output_usd": 1.1,
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
}
