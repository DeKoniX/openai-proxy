# AGENTS.md - internal/server

**Scope:** HTTP handlers, reverse proxy logic, token/cost calculations

## OVERVIEW
Core HTTP server handling proxy forwarding, admin API endpoints, and request/response logging with cost tracking.

## WHERE TO LOOK
| Task | Symbol | Notes |
|------|--------|-------|
| Add admin endpoint | Handler() | Add mux.HandleFunc before proxy handler |
| Modify proxy behavior | proxyHandler | Request routing, body capture |
| Change upstream forwarding | buildReverseProxy | ModifyResponse, ErrorHandler, Director |
| Update cost calculation | calculateUsageCosts | Token pricing logic |
| Add response processing | loggingBody | Custom ReadCloser for response capture |
| Handle streaming | streamingLogger | SSE streaming response handler |

## KEY PATTERNS

### Adding API Endpoints
```go
mux.HandleFunc("/admin/api/new-endpoint", s.handleNewEndpoint)
```

Route registration order matters - more specific paths first.

### Reverse Proxy Customization
The buildReverseProxy method creates a customized httputil.ReverseProxy:
- **ModifyResponse**: Captures response headers/body, handles gzip
- **ErrorHandler**: Logs upstream errors, saves failed request logs
- **Director**: Sets upstream host, injects API keys

### Request Context
Log entries flow through context:
```go
ctx := context.WithValue(req.Context(), logEntryKey{}, entry)
```

Extract with getLogEntry(ctx) in response handlers.

### Token Counting
- Request tokens: countTokens() on request body
- Response tokens: accumulateTokens() during streaming read
- Provider usage: extractUsageFromBody() parses OpenAI-style usage fields

### Cost Calculation
```go
// Prices are $ per 1M tokens
inputUSD = pricePerMillion * tokens / 1_000_000
rub = usd * usdToRubRate
```

## ANTI-PATTERNS

- **DON'T** block in proxyHandler - use goroutines for async operations (saveLogAsync)
- **DON'T** modify response after ModifyResponse returns (streaming breaks)
- **DON'T** forget to close response bodies (loggingBody.Close handles this)
- **DON'T** use global state - Server fields are shared, use mutexes (routesMu)

## STREAMING NOTES

SSE streams use streamingLogger which:
1. Counts tokens as chunks arrive
2. Calculates costs on Close()
3. Saves log asynchronously

Do not attempt to buffer streaming responses - pass through to client immediately.

## TESTING

- Main test: server_test.go (table-driven)
- Mock Store interface for isolated handler tests
- Test route matching with various path prefixes
