package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/dekonix/openai-proxy/internal/config"
	"github.com/dekonix/openai-proxy/internal/models"
	"github.com/dekonix/openai-proxy/internal/storage"
)

type logEntryKey struct{}

// Store defines the persistence operations required by the server.
type Store interface {
	SaveLog(ctx context.Context, entry *models.APILog) error
	ListRecent(ctx context.Context, limit int) ([]models.APILog, error)
	GetBody(ctx context.Context, id int64) (*models.APILog, error)
	ListProxies(ctx context.Context) ([]models.Proxy, error)
	CreateProxy(ctx context.Context, proxy *models.Proxy) error
	UpdateProxy(ctx context.Context, id int64, upd storage.ProxyUpdate) error
	ResetProxyUsage(ctx context.Context, id int64) error
	DeleteProxy(ctx context.Context, id int64) error
}

type proxyRoute struct {
	def    *models.Proxy
	target *url.URL
	proxy  *httputil.ReverseProxy
}

// Server handles HTTP traffic for proxying and admin endpoints.
type Server struct {
	cfg          *config.Config
	store        Store
	usdToRubRate float64

	routesMu sync.RWMutex
	routes   []*proxyRoute
}

// New builds a Server with proxy and routes ready.
func New(cfg *config.Config, store Store) (*Server, error) {
	s := &Server{cfg: cfg, store: store, usdToRubRate: cfg.USDRUBRate}

	if err := s.reloadRoutes(context.Background()); err != nil {
		return nil, err
	}

	if len(s.routes) == 0 && cfg.UpstreamURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		proxy := &models.Proxy{
			Name:                "Default",
			PathPrefix:          normalizePathPrefix("/"),
			UpstreamURL:         cfg.UpstreamURL,
			UpstreamKey:         cfg.UpstreamKey,
			TokenPriceInputUSD:  0,
			TokenPriceOutputUSD: 0,
		}

		if err := store.CreateProxy(ctx, proxy); err != nil && !storage.IsUniqueViolation(err) {
			return nil, fmt.Errorf("create default proxy: %w", err)
		}

		if err := s.reloadRoutes(context.Background()); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// Handler returns the configured HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/logs", s.handleLogsPage)
	mux.HandleFunc("/admin/logs/", s.handleLogDetails)
	mux.HandleFunc("/admin/api/logs", s.handleLogsAPI)
	mux.HandleFunc("/admin/api/proxies", s.handleProxiesAPI)
	mux.HandleFunc("/admin/api/proxies/", s.handleProxyItemAPI)

	// Proxy everything else.
	mux.HandleFunc("/", s.proxyHandler)

	return mux
}

func (s *Server) proxyHandler(rw http.ResponseWriter, req *http.Request) {
	log.Printf("incoming request %s %s", req.Method, req.URL.Path)

	body, err := io.ReadAll(req.Body)
	if err != nil {
		log.Printf("read request body error: %v", err)
		http.Error(rw, "failed to read request", http.StatusBadRequest)
		return
	}
	req.Body.Close()

	route, trimmedPath := s.matchRoute(req.URL.Path)
	if route == nil {
		log.Printf("no proxy configured for %s %s", req.Method, req.URL.Path)
		http.Error(rw, "proxy not configured", http.StatusBadGateway)
		return
	}

	log.Printf("route matched prefix=%s upstream=%s", route.def.PathPrefix, route.def.UpstreamURL)

	entry := &models.APILog{
		ProxyID:         route.def.ID,
		ProxyName:       route.def.Name,
		ProxyPathPrefix: route.def.PathPrefix,
		Method:          req.Method,
		Path:            req.URL.Path,
		Query:           req.URL.RawQuery,
		RequestHeaders:  serializeHeaders(req.Header, true),
		RequestBody:     body,
		RequestTokens:   countTokens(body),
	}

	ctx := context.WithValue(req.Context(), logEntryKey{}, entry)

	proxyReq := req.Clone(ctx)
	proxyReq.Body = io.NopCloser(bytes.NewReader(body))
	proxyReq.ContentLength = int64(len(body))
	proxyReq.URL.Path = trimmedPath
	proxyReq.URL.RawPath = ""
	proxyReq.URL.RawQuery = req.URL.RawQuery

	if route.def.UpstreamKey != "" {
		proxyReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", route.def.UpstreamKey))
	}

	route.proxy.ServeHTTP(rw, proxyReq)
}

func (s *Server) handleLogsPage(rw http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/admin/logs" {
		http.NotFound(rw, req)
		return
	}

	if req.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	http.ServeFile(rw, req, "web/static/index.html")
}

func (s *Server) handleLogDetails(rw http.ResponseWriter, req *http.Request) {
	if !strings.HasPrefix(req.URL.Path, "/admin/logs/") {
		http.NotFound(rw, req)
		return
	}
	idStr := strings.TrimPrefix(req.URL.Path, "/admin/logs/")
	if idStr == "" {
		http.NotFound(rw, req)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(rw, "invalid id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	entry, err := s.store.GetBody(ctx, id)
	if err != nil {
		http.Error(rw, "log not found", http.StatusNotFound)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(entry)
}

func (s *Server) handleLogsAPI(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	limit := 50
	if n := req.URL.Query().Get("limit"); n != "" {
		if parsed, err := strconv.Atoi(n); err == nil {
			limit = parsed
		}
	}

	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	logs, err := s.store.ListRecent(ctx, limit)
	if err != nil {
		http.Error(rw, "failed to load logs", http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(logs)
}

func (s *Server) handleProxiesAPI(rw http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		s.listProxies(rw, req)
	case http.MethodPost:
		s.createProxy(rw, req)
	default:
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleProxyItemAPI(rw http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/admin/api/proxies/")
	if path == "" {
		http.NotFound(rw, req)
		return
	}

	parts := strings.Split(path, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(rw, "invalid proxy id", http.StatusBadRequest)
		return
	}

	if len(parts) == 1 {
		switch req.Method {
		case http.MethodPatch:
			s.updateProxy(rw, req, id)
		case http.MethodDelete:
			s.deleteProxy(rw, req, id)
		default:
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	if len(parts) == 2 && parts[1] == "reset" {
		if req.Method == http.MethodPost {
			s.resetProxy(rw, req, id)
			return
		}
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	http.NotFound(rw, req)
}

func (s *Server) listProxies(rw http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	proxies, err := s.store.ListProxies(ctx)
	if err != nil {
		http.Error(rw, "failed to load proxies", http.StatusInternalServerError)
		return
	}

	var totalInputTokens, totalOutputTokens int64
	var totalInputUSD, totalOutputUSD float64
	var totalInputRUB, totalOutputRUB float64

	for i := range proxies {
		if proxies[i].UpstreamKey != "" {
			proxies[i].UpstreamKey = "***"
		}
		proxies[i].PathPrefix = normalizePathPrefix(proxies[i].PathPrefix)
		totalInputTokens += proxies[i].UsageInputTokens
		totalOutputTokens += proxies[i].UsageOutputTokens
		totalInputUSD += proxies[i].UsageInputCostUSD
		totalOutputUSD += proxies[i].UsageOutputCostUSD
		totalInputRUB += proxies[i].UsageInputCostRUB
		totalOutputRUB += proxies[i].UsageOutputCostRUB
	}

	response := struct {
		Proxies            []models.Proxy `json:"proxies"`
		TotalInputTokens   int64          `json:"total_input_tokens"`
		TotalOutputTokens  int64          `json:"total_output_tokens"`
		TotalInputCostUSD  float64        `json:"total_input_cost_usd"`
		TotalOutputCostUSD float64        `json:"total_output_cost_usd"`
		TotalInputCostRUB  float64        `json:"total_input_cost_rub"`
		TotalOutputCostRUB float64        `json:"total_output_cost_rub"`
	}{
		Proxies:            proxies,
		TotalInputTokens:   totalInputTokens,
		TotalOutputTokens:  totalOutputTokens,
		TotalInputCostUSD:  totalInputUSD,
		TotalOutputCostUSD: totalOutputUSD,
		TotalInputCostRUB:  totalInputRUB,
		TotalOutputCostRUB: totalOutputRUB,
	}

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(response)
}

func (s *Server) createProxy(rw http.ResponseWriter, req *http.Request) {
	type payload struct {
		Name                string  `json:"name"`
		PathPrefix          string  `json:"path_prefix"`
		UpstreamURL         string  `json:"upstream_url"`
		UpstreamKey         string  `json:"upstream_key"`
		TokenPriceInputUSD  float64 `json:"token_price_input_usd"`
		TokenPriceOutputUSD float64 `json:"token_price_output_usd"`
	}

	var body payload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, "invalid json", http.StatusBadRequest)
		return
	}

	prefix := normalizePathPrefix(body.PathPrefix)
	if prefix == "" {
		http.Error(rw, "path_prefix is required", http.StatusBadRequest)
		return
	}

	if body.UpstreamURL == "" {
		http.Error(rw, "upstream_url is required", http.StatusBadRequest)
		return
	}

	target, err := url.Parse(body.UpstreamURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		http.Error(rw, "upstream_url must be a valid absolute URL", http.StatusBadRequest)
		return
	}

	proxy := &models.Proxy{
		Name:                strings.TrimSpace(body.Name),
		PathPrefix:          prefix,
		UpstreamURL:         target.String(),
		UpstreamKey:         strings.TrimSpace(body.UpstreamKey),
		TokenPriceInputUSD:  body.TokenPriceInputUSD,
		TokenPriceOutputUSD: body.TokenPriceOutputUSD,
	}

	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	if err := s.store.CreateProxy(ctx, proxy); err != nil {
		if storage.IsUniqueViolation(err) {
			http.Error(rw, "path_prefix already exists", http.StatusConflict)
			return
		}
		log.Printf("failed to create proxy prefix=%s: %v", prefix, err)
		http.Error(rw, "failed to save proxy", http.StatusInternalServerError)
		return
	}

	if err := s.reloadRoutes(context.Background()); err != nil {
		log.Printf("reload routes: %v", err)
	}

	proxy.UpstreamKey = "***"
	proxy.PathPrefix = normalizePathPrefix(proxy.PathPrefix)

	log.Printf("created proxy id=%d prefix=%s upstream=%s", proxy.ID, proxy.PathPrefix, proxy.UpstreamURL)

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusCreated)
	json.NewEncoder(rw).Encode(proxy)
}

func (s *Server) updateProxy(rw http.ResponseWriter, req *http.Request, id int64) {
	type payload struct {
		Name                *string  `json:"name"`
		UpstreamKey         *string  `json:"upstream_key"`
		TokenPriceInputUSD  *float64 `json:"token_price_input_usd"`
		TokenPriceOutputUSD *float64 `json:"token_price_output_usd"`
	}

	var body payload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, "invalid json", http.StatusBadRequest)
		return
	}

	updates := storage.ProxyUpdate{
		Name:                body.Name,
		TokenPriceInputUSD:  body.TokenPriceInputUSD,
		TokenPriceOutputUSD: body.TokenPriceOutputUSD,
		UpstreamKey:         body.UpstreamKey,
	}

	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	if err := s.store.UpdateProxy(ctx, id, updates); err != nil {
		if errors.Is(err, storage.ErrNoFieldsToUpdate) {
			http.Error(rw, "no fields to update", http.StatusBadRequest)
			return
		}
		log.Printf("failed to update proxy %d: %v", id, err)
		http.Error(rw, "failed to update proxy", http.StatusInternalServerError)
		return
	}

	if err := s.reloadRoutes(context.Background()); err != nil {
		log.Printf("reload routes: %v", err)
	}

	log.Printf("updated proxy %d", id)

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(map[string]string{"status": "ok"})
}

func (s *Server) resetProxy(rw http.ResponseWriter, req *http.Request, id int64) {
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	if err := s.store.ResetProxyUsage(ctx, id); err != nil {
		log.Printf("failed to reset usage for proxy %d: %v", id, err)
		http.Error(rw, "failed to reset usage", http.StatusInternalServerError)
		return
	}

	log.Printf("reset usage for proxy %d", id)
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(map[string]string{"status": "ok"})
}

func (s *Server) deleteProxy(rw http.ResponseWriter, req *http.Request, id int64) {
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	if err := s.store.DeleteProxy(ctx, id); err != nil {
		log.Printf("failed to delete proxy %d: %v", id, err)
		http.Error(rw, "failed to delete proxy", http.StatusInternalServerError)
		return
	}

	if err := s.reloadRoutes(context.Background()); err != nil {
		log.Printf("reload routes: %v", err)
	}

	log.Printf("deleted proxy %d", id)
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(map[string]string{"status": "deleted"})
}

func (s *Server) buildReverseProxy(route *proxyRoute) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(route.target)

	proxy.ModifyResponse = func(resp *http.Response) error {
		entry := getLogEntry(resp.Request.Context())
		if entry == nil {
			return nil
		}

		entry.ResponseStatus = resp.StatusCode
		entry.ResponseHeaders = serializeHeaders(resp.Header, false)

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			entry.ErrorMessage = fmt.Sprintf("read upstream response: %v", err)
			resp.Body.Close()
			return err
		}
		resp.Body.Close()
		entry.ResponseBody = body
		entry.ResponseTokens = countTokens(body)
		entry.TotalTokens = entry.RequestTokens + entry.ResponseTokens
		entry.CostInputUSD, entry.CostInputRUB = s.calculateCost(route.def.TokenPriceInputUSD, entry.RequestTokens)
		entry.CostOutputUSD, entry.CostOutputRUB = s.calculateCost(route.def.TokenPriceOutputUSD, entry.ResponseTokens)
		entry.CostTotalUSD = entry.CostInputUSD + entry.CostOutputUSD
		entry.CostTotalRUB = entry.CostInputRUB + entry.CostOutputRUB
		log.Printf("proxy success %s %s status=%d tokens_in=%d tokens_out=%d", entry.Method, entry.Path, entry.ResponseStatus, entry.RequestTokens, entry.ResponseTokens)

		// Replace response body so it can be sent to client unchanged.
		resp.Body = io.NopCloser(bytes.NewBuffer(body))
		resp.ContentLength = int64(len(body))
		if len(body) == 0 {
			resp.Header.Del("Content-Length")
		} else {
			resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
		}

		ctx, cancel := context.WithTimeout(resp.Request.Context(), 5*time.Second)
		defer cancel()
		if err := s.store.SaveLog(ctx, entry); err != nil {
			log.Printf("failed to save log entry: %v", err)
			return fmt.Errorf("save log: %w", err)
		}
		return nil
	}

	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		entry := getLogEntry(req.Context())
		if entry != nil {
			entry.ErrorMessage = err.Error()
			entry.ResponseStatus = http.StatusBadGateway
			entry.ResponseHeaders = ""
			entry.ResponseTokens = 0
			entry.TotalTokens = entry.RequestTokens
			entry.CostInputUSD, entry.CostInputRUB = s.calculateCost(route.def.TokenPriceInputUSD, entry.RequestTokens)
			entry.CostOutputUSD, entry.CostOutputRUB = 0, 0
			entry.CostTotalUSD = entry.CostInputUSD
			entry.CostTotalRUB = entry.CostInputRUB

			ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
			if saveErr := s.store.SaveLog(ctx, entry); saveErr != nil {
				log.Printf("failed to save log entry after upstream error: %v", saveErr)
				err = fmt.Errorf("%v; additionally failed to save log: %w", err, saveErr)
			}
			cancel()
		}
		log.Printf("upstream error for %s %s: %v", req.Method, req.URL.Path, err)
		http.Error(rw, "upstream error", http.StatusBadGateway)
	}

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		if route.def.UpstreamKey != "" {
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", route.def.UpstreamKey))
		}
	}

	return proxy
}

func (s *Server) reloadRoutes(ctx context.Context) error {
	proxies, err := s.store.ListProxies(ctx)
	if err != nil {
		return fmt.Errorf("load proxies: %w", err)
	}

	routes := make([]*proxyRoute, 0, len(proxies))
	for i := range proxies {
		def := proxies[i]
		def.PathPrefix = normalizePathPrefix(def.PathPrefix)

		target, err := url.Parse(def.UpstreamURL)
		if err != nil || target.Scheme == "" || target.Host == "" {
			log.Printf("skip proxy id=%d: invalid upstream url: %v", def.ID, err)
			continue
		}

		def.UpstreamURL = target.String()

		route := &proxyRoute{
			def:    &def,
			target: target,
		}
		route.proxy = s.buildReverseProxy(route)
		routes = append(routes, route)
	}

	s.routesMu.Lock()
	s.routes = routes
	s.routesMu.Unlock()

	return nil
}

func (s *Server) matchRoute(path string) (*proxyRoute, string) {
	s.routesMu.RLock()
	defer s.routesMu.RUnlock()

	for _, route := range s.routes {
		prefix := route.def.PathPrefix
		if !strings.HasPrefix(path, prefix) {
			continue
		}

		if len(path) > len(prefix) && prefix != "/" && path[len(prefix)] != '/' {
			continue
		}

		remainder := strings.TrimPrefix(path, prefix)
		if remainder == "" {
			remainder = "/"
		} else if remainder[0] != '/' {
			remainder = "/" + remainder
		}

		return route, remainder
	}

	return nil, ""
}

func (s *Server) calculateCost(pricePerMillion float64, tokens int) (float64, float64) {
	if pricePerMillion <= 0 || tokens <= 0 {
		return 0, 0
	}
	usd := pricePerMillion * float64(tokens) / 1_000_000.0
	rub := usd * s.usdToRubRate
	return usd, rub
}

func getLogEntry(ctx context.Context) *models.APILog {
	val := ctx.Value(logEntryKey{})
	if val == nil {
		return nil
	}
	entry, _ := val.(*models.APILog)
	return entry
}

func serializeHeaders(h http.Header, redactAuth bool) string {
	if len(h) == 0 {
		return ""
	}
	var builder strings.Builder
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		values := h[key]
		for _, value := range values {
			if redactAuth && strings.EqualFold(key, "Authorization") {
				value = "***"
			}
			builder.WriteString(key)
			builder.WriteString(": ")
			builder.WriteString(value)
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

func normalizePathPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return ""
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if len(prefix) > 1 {
		prefix = strings.TrimSuffix(prefix, "/")
		if prefix == "" {
			prefix = "/"
		}
	}
	return prefix
}

func countTokens(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return 0
	}

	split := func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}

	tokens := strings.FieldsFunc(text, split)
	return len(tokens)
}
