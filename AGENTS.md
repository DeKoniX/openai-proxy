# AGENTS.md - Coding Guidelines for OpenAI Proxy

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