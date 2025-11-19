# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**agentsdk-go** is a from-scratch, production-ready Go Agent SDK that mirrors Claude Code's 7 core capabilities with a pure architecture approach benchmarked to Claude Code's stack. This SDK targets CLI, CI/CD, and enterprise platforms, prioritizing KISS-friendly modularity, a zero-dependency core, and the middleware interception system that extends Claude Code with a unique innovation.

**Key metrics**:
- ~6k LOC core code
- 91.1% average test coverage across new modules
- Zero external dependencies in core packages
- Agent core loop <300 lines

## Architecture

### Pure Claude Code Architecture (13 independent packages)

```
Core Layer (6 modules):
├── agent/       - Agent core loop (<300 lines)
├── middleware/  - 6-point interception system
├── model/       - Anthropic model adapter
├── tool/        - Tool registry & execution
├── message/     - In-memory message history
└── api/         - Unified SDK interface

Claude Code Features (7 modules):
├── config/      - Configuration loading & hot-reload
├── plugins/     - Plugin system with signature verification
├── core/
│   ├── events/  - Event bus
│   └── hooks/   - Hooks executor
├── sandbox/     - Filesystem & network isolation
├── mcp/         - MCP client
├── runtime/
│   ├── skills/     - Skills management
│   ├── subagents/  - Subagents management
│   └── commands/   - Slash commands parser
└── security/    - Security utilities
```

### 6 Middleware Interception Points

The SDK provides a complete request/response interception chain at every critical point:

```
User Request
     ↓
[before_agent]  ← Request validation, rate limiting, audit logging
     ↓
Agent Loop
     ↓
[before_model]  ← Prompt enhancement, context trimming
     ↓
Model.Generate
     ↓
[after_model]   ← Result filtering, safety checks
     ↓
[before_tool]   ← Tool call validation, parameter checking
     ↓
Tool.Execute
     ↓
[after_tool]    ← Result post-processing, error handling
     ↓
[after_agent]   ← Response formatting, metrics reporting
     ↓
User Response
```

## Common Commands

### Build & Test

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run core module tests only
go test ./pkg/agent/... ./pkg/middleware/... ./pkg/model/...

# Run integration tests
go test ./test/integration/...

# Build CLI tool
make agentctl
# or
go build -o bin/agentctl ./cmd/agentctl

# Lint
make lint
# or
golangci-lint run
```

### Running Examples

```bash
# CLI example (auto-resolves project root)
cd examples/cli
go run .

# HTTP server example
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/http
go run .
# Server listens on :8080

# MCP client example
cd examples/mcp
go run .
```

### HTTP API Endpoints

When running the HTTP example:

- `POST /v1/run` - Blocking request, waits for full response
- `POST /v1/run/stream` - SSE streaming with real-time progress
- `POST /v1/tools/execute` - Direct tool execution

Example streaming request:
```bash
curl -N -X POST http://localhost:8080/v1/run/stream \
  -H 'Content-Type: application/json' \
  -d '{"prompt": "列出当前目录", "session_id": "demo"}'
```

### Development Workflow

```bash
# Clean build artifacts
make clean

# Install CLI globally
make install

# Generate coverage report
make coverage
```

## Code Architecture Details

### Agent Core Loop

**Location**: `pkg/agent/agent.go`

The agent loop is intentionally kept under 300 lines. Key points:
- Uses context for cancellation and timeout
- Executes middleware at 6 distinct stages
- Limits iterations via `MaxIterations` option
- Returns `ModelOutput` with content, tool calls, and completion status

### Model Adapters

**Location**: `pkg/model/`

Currently supports:
- **Anthropic** (`anthropic.go`) - Primary provider with Claude models
- **OpenAI** (compatibility layer)

Adapters convert between SDK's generic message format and provider-specific APIs.

### Tool System

**Location**: `pkg/tool/`

Key components:
- `Registry` - Thread-safe tool registration
- `Tool` interface - Name, Description, Schema, Execute
- `Validator` - JSON Schema validation before execution
- Built-in tools: bash, file operations, grep, glob
- MCP client support for external tools

**Important**: Tool execution validates parameters against JSON Schema before invocation to catch errors early.

### Middleware System

**Location**: `pkg/middleware/`

Chain-of-responsibility pattern with 6 stages:
- `BeforeAgent` / `AfterAgent` - Request/response boundary
- `BeforeModel` / `AfterModel` - Model generation boundary
- `BeforeTool` / `AfterTool` - Tool execution boundary

State is passed through `middleware.State` with a `Values` map for cross-middleware data sharing.

### Message History

**Location**: `pkg/message/`

In-memory message store with:
- LRU eviction policy (configurable via `MaxSessions`)
- Per-session history tracking
- Thread-safe access
- Supports user, assistant, system, and tool result messages

### Configuration

**Location**: `pkg/config/`

The SDK follows Claude Code's `.claude/` directory structure:
```
.claude/
├── config.yaml       # Project configuration
├── skills/           # Skills definitions
├── commands/         # Slash commands
├── agents/           # Subagents definitions
└── plugins/          # Plugin directory
```

Hot-reload support via `fsnotify` for configuration changes.

### Security & Sandbox

**Location**: `pkg/sandbox/`, `pkg/security/`

Three-layer defense:
1. **Path whitelist** - Restricts filesystem access
2. **Symlink resolution** - Prevents path traversal via symbolic links
3. **Command validation** - Blocks dangerous commands (rm -rf, etc.)

Network isolation via allow-list for outbound connections.

## Testing Strategy

### Test Coverage Requirements

- Core modules: ≥90% coverage
- New features: Must include tests
- Use table-driven tests for multiple scenarios

### Test File Patterns

Tests are co-located with implementation:
- Unit tests: `*_test.go` alongside source files
- Additional edge case tests: `*_additional_test.go`
- Integration tests: `test/integration/`

### Running Specific Tests

```bash
# Test a single package
go test ./pkg/agent

# Test with verbose output
go test -v ./pkg/middleware

# Run specific test
go test -run TestAgent_Run ./pkg/agent

# Benchmark tests
go test -bench=. ./pkg/agent
```

## Code Style & Conventions

### File Size Limit

**CRITICAL**: Keep files under 500 lines. This project explicitly avoids the "巨型单文件" anti-pattern found in other SDKs.

If a file exceeds 500 lines:
1. Split by responsibility (e.g., separate validators, helpers)
2. Extract interfaces to their own files
3. Move test helpers to `*_helpers_test.go`

### Naming Conventions

- Interfaces: `Model`, `Tool`, `ToolExecutor` (noun)
- Implementations: `AnthropicProvider`, `BashTool` (concrete)
- Options structs: Use functional options pattern
- Errors: Declare as package-level `var ErrXxx = errors.New(...)`

### Concurrency

- Use `sync.RWMutex` for shared state (e.g., Registry)
- Context-aware: Always respect `ctx.Done()` in loops
- No naked goroutines: Use errgroup or manage lifecycle explicitly

### Error Handling

```go
// Wrap errors with context
return fmt.Errorf("execute tool %s: %w", name, err)

// Check for specific errors
if errors.Is(err, ErrMaxIterations) { ... }

// Declare sentinel errors at package level
var ErrNilModel = errors.New("agent: model is nil")
```

## HTTP API Notes

The HTTP example (`examples/http/`) demonstrates:
- **SSE Streaming**: Full Anthropic-compatible event stream
- **Character-by-character output**: Real-time text streaming via `content_block_delta` events
- **Tool execution visibility**: `tool_execution_start/stop` events
- **Heartbeat**: 15s ping events to prevent connection drops
- **Sandbox per-request**: Override filesystem/network policies in request body

Configuration via environment variables (see `examples/http/README.md`).

## MCP Integration

**Location**: `pkg/mcp/`

Supports Model Context Protocol for external tools:
- stdio transport (for local processes)
- SSE transport (for HTTP servers)
- Automatic tool registration from MCP servers
- Configurable via `.claude/config.yaml` or `--mcp` CLI flag

Example:
```yaml
# .claude/config.yaml
mcp:
  servers:
    - name: "my-server"
      command: "node"
      args: ["server.js"]
```

## Entry Points

The SDK supports three entry point modes:
- **CLI**: Interactive command-line usage
- **CI**: Continuous integration environments
- **Platform**: Embedded in larger applications

Set via `api.Options.EntryPoint`.

## API Usage Patterns

### Basic Usage

```go
provider := &model.AnthropicProvider{ModelName: "claude-sonnet-4-5"}
runtime, err := api.New(ctx, api.Options{
    ProjectRoot:   ".",
    ModelFactory:  provider,
})
defer runtime.Close()

result, err := runtime.Run(ctx, api.Request{
    Prompt:    "Your task here",
    SessionID: "session-123",
})
```

### Streaming Usage

```go
events := runtime.RunStream(ctx, api.Request{...})
for event := range events {
    switch event.Type {
    case "model_delta":
        // Handle incremental text
    case "tool_call":
        // Handle tool execution
    }
}
```

### Adding Custom Tools

Implement `tool.Tool` interface:
```go
type CustomTool struct{}
func (t *CustomTool) Name() string { return "my_tool" }
func (t *CustomTool) Description() string { return "..." }
func (t *CustomTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{...} }
func (t *CustomTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    // Implementation
}
```

Register before runtime creation via config or programmatically.

## Performance Considerations

- **Zero allocations in hot paths**: Agent loop avoids unnecessary allocations
- **LRU session cache**: Prevents unbounded memory growth
- **Streaming preferred**: Use `RunStream` for long-running tasks to get immediate feedback
- **Context timeout**: Always set reasonable timeouts on context

## Common Pitfalls

1. **Nil Model Check**: Agent creation requires a non-nil model provider
2. **Session ID Uniqueness**: Reusing session IDs appends to history; use unique IDs for isolation
3. **Sandbox Path Resolution**: Always use absolute paths; symlinks are resolved before validation
4. **Tool Parameter Validation**: Schema validation happens before execution—define schemas accurately
5. **Context Cancellation**: Respect context cancellation in custom tools and middleware

## Documentation

Key documentation files:
- `README.md` - Project overview, features, quick start
- `docs/architecture.md` - Detailed architecture analysis (横向对比 16 个项目)
- `docs/getting-started.md` - Step-by-step tutorial
- `docs/api-reference.md` - API documentation
- `docs/security.md` - Security best practices
- `examples/http/README.md` - HTTP API guide
- `.claude/specs/claude-code-rewrite/` - Development plans and reports

## Project Principles

This codebase follows Linus Torvalds' philosophy:
- **KISS** - Keep It Simple: Single responsibility, core files <500 lines
- **YAGNI** - You Aren't Gonna Need It: Zero dependencies, extend as needed
- **Never Break Userspace** - API stability, backward compatibility
- **大道至简** - Simple interfaces, refined implementation

## Important File Locations

- Agent core: `pkg/agent/agent.go`
- Tool registry: `pkg/tool/registry.go`
- Model providers: `pkg/model/anthropic.go`, `pkg/model/openai.go`
- Middleware chain: `pkg/middleware/chain.go`
- API entry point: `pkg/api/agent.go`
- CLI tool: `cmd/cli/main.go`
- HTTP server example: `examples/http/main.go`

When adding new features, maintain the modular structure and keep test coverage ≥90%.
