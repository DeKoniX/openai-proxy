# AGENTS.md - Coding Guidelines for OpenAI Proxy

**Generated:** 2026-03-15  
**Commit:** HEAD  
**Branch:** main

## OVERVIEW
OpenAI API proxy server with PostgreSQL logging and token cost tracking. Routes requests to configured upstream endpoints while recording request/response logs and calculating usage costs.

## STRUCTURE
```
.
├── cmd/server/          # Application entry point
├── internal/
│   ├── config/          # Environment-based config
│   ├── models/          # Domain types (Proxy, APILog, StatPoint)
│   ├── server/          # HTTP handlers + reverse proxy logic
│   └── storage/         # PostgreSQL persistence
├── web/static/          # Admin UI HTML files
└── openspec/            # OpenSpec skill configs
```

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Add new API endpoint | internal/server/server.go | Add to Handler() mux |
| Modify proxy behavior | internal/server/server.go | proxyHandler, buildReverseProxy |
| Change database schema | internal/storage/postgres.go | runMigrations() |
| Add new config option | internal/config/config.go | Add to Config struct + Load() |
| Update data models | internal/models/models.go | All DB-mapped types |
| Modify admin UI | web/static/*.html | Static HTML files |

## Build/Lint/Test Commands

### Building
- Build server: `go build ./cmd/server`
- Run server: `go run ./cmd/server`

### Testing
- Run all tests: `go test ./...`
- Run single test: `go test -run TestName ./package/path`
- Example: `go test -run TestListProxies ./internal/server`

### Linting & Formatting
- Check formatting: `gofmt -d .`
- Vet for issues: `go vet ./...`
- Advanced linting: `staticcheck ./...`
- Comprehensive linting: `golangci-lint run`

## CODE MAP

| Symbol | Type | File | Role |
|--------|------|------|------|
| Server | struct | server.go | HTTP handler + reverse proxy orchestrator |
| Store | interface | server.go | Persistence abstraction |
| proxyRoute | struct | server.go | Route definition + upstream target |
| Config | struct | config.go | Environment config holder |
| Proxy | struct | models.go | Upstream routing rule |
| APILog | struct | models.go | Request/response log entry |
| StatPoint | struct | models.go | Aggregated statistics |
| storage.Store | struct | postgres.go | PostgreSQL implementation |

## Code Style Guidelines

### Language & Version
- Go 1.25.1
- Follow standard Go formatting (`gofmt`)

### Package Structure
- `cmd/` - Main applications
- `internal/` - Private application code
- Package names: lowercase, single word

### Imports
```go
import (
    "context"
    "fmt"
    "net/http"

    "github.com/third/party/package"

    "github.com/dekonix/openai-proxy/internal/models"
)
```

### Naming Conventions
- Exported: PascalCase (types, functions, constants)
- Unexported: camelCase (variables, functions)
- Acronyms: HTTP, URL, API, ID (not Http, Url, Api, Id)

### Struct Tags
- JSON: snake_case
- Example: `CreatedAt time.Time `json:"created_at"`

### Error Handling
- Wrap errors with `fmt.Errorf`: `fmt.Errorf("operation failed: %w", err)`
- Return early on errors
- Use context for cancellation

### Comments
- Exported types/functions: Brief description
- Implementation comments only when complex logic requires explanation

### Testing
- Use table-driven tests
- Helper functions: call `t.Helper()`
- Mock interfaces for dependencies
- Test package: same as implementation package

### Types & Interfaces
- Use `any` instead of `interface{}` (Go 1.18+)
- Define interfaces for dependencies
- Use struct composition
- Prefer concrete types over interfaces in function parameters