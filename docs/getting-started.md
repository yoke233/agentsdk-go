# Getting Started

1. Install Go 1.23 or newer.
2. Clone this repository and run `go env -w GOPRIVATE=github.com/yourusername` if needed.
3. Execute `make build` to confirm the scaffold compiles.
4. Explore `examples/` to see how the agent runtime is expected to be wired.

Document all deviations from `docs/architecture.md` as you begin implementing features.

---
## 快速开始

### 环境准备
- **Go 版本**：保持在 Go 1.23+，与 `go.mod` 对齐，保证 `toolchain` 行为一致。
- **项目路径**：确保在 `$(pwd)/agentsdk-go` 下执行命令，便于引用本指南中的相对路径，例如 `pkg/agent/agent.go`。
- **API Key**：示例默认读取 `ANTHROPIC_API_KEY` 环境变量，务必在运行前导出。

### 安装 SDK
1. 拉取源代码：
   ```bash
   git clone https://github.com/cexll/agentsdk-go.git
   cd agentsdk-go
   ```
2. 确认可以构建：
   ```bash
   make build
   ```
3. 运行核心单测感受基础设施：
   ```bash
   go test ./pkg/agent ./pkg/middleware
   ```

### Hello Agent 示例
以下示例保存在 `examples/getting-started/main.go`（可自行创建）。它展示了如何以极简方式组合 `pkg/agent/agent.go` 和 `pkg/middleware/chain.go`。

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/cexll/agentsdk-go/pkg/agent"
    "github.com/cexll/agentsdk-go/pkg/api"
    "github.com/cexll/agentsdk-go/pkg/middleware"
    "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
    ctx := context.Background()

    // 1. 配置模型提供者（参见 pkg/model/anthropic.go）
    provider := model.NewAnthropicProvider(
        model.WithAPIKeyFromEnv("ANTHROPIC_API_KEY"), // 使用环境变量，避免硬编码
        model.WithModel("claude-sonnet-4-5"),         // 指定默认模型
    )

    // 2. 组装 middleware 链（参见 pkg/middleware/types.go）
    logging := middleware.Funcs{
        Identifier: "console-logger",
        OnBeforeAgent: func(ctx context.Context, st *middleware.State) error {
            log.Printf("[agent] iteration=%d", st.Iteration)
            return nil
        },
        OnAfterAgent: func(ctx context.Context, st *middleware.State) error {
            log.Printf("[agent] result=%s", st.ModelOutput.Content)
            return nil
        },
    }

    chain := middleware.NewChain([]middleware.Middleware{logging}, middleware.WithTimeout(2*time.Second))

    // 3. 创建统一 API Runtime（参见 pkg/api/agent.go）
    runtime, err := api.New(ctx, api.Options{
        ProjectRoot:   ".",
        ModelProvider: provider,
        Middleware:    chain,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer runtime.Close() // 释放连接、缓冲区

    // 4. 发起一次请求，输出结果
    output, err := runtime.Run(ctx, api.Request{Prompt: "Say hello in one sentence."})
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("assistant: %s", output.Content)
}
```

### 运行并验证
```bash
export ANTHROPIC_API_KEY=sk-ant-...
go run ./examples/getting-started
```
- 若需调试 Agent 核心循环，可将 `AGENT_DEBUG=1`（由 `.claude/settings.local.json` 内的 `debug` 选项控制）并重跑。
- 推荐配合 `go test -run TestAgent_Run ./pkg/agent`，确保示例与核心逻辑保持一致。

## 核心概念

| 概念 | 定义 | 关键文件 | 使用要点 |
| ---- | ---- | -------- | -------- |
| Agent | 核心循环，负责调用模型、调度工具和 middleware。 | `pkg/agent/agent.go` | `Agent.Run` 会在 6 个 Stage 之间循环，务必传入非空 `Model`。 |
| Model | 实现 `Generate` 方法的提供者，例如 Anthropic。 | `pkg/model/anthropic.go` | 返回 `ModelOutput{Content, ToolCalls, Done}`，不要返回 `nil`。 |
| Tool | 实现 `tool.Tool` 接口并注册在执行器内。 | `pkg/tool/registry.go` | 在 schema 中声明参数，防止 `StageBeforeTool` 拦截失败。 |
| Context | `pkg/agent/context.go` 中的对话状态，记录历史、工具结果。 | `pkg/agent/context.go` | 通过 middleware 共享在 `middleware.State.Agent` 字段。 |
| Middleware | 六段式拦截链，贯穿 Agent 生命周期。 | `pkg/middleware/chain.go`, `pkg/middleware/types.go` | 可组合多个 `Middleware`，出现错误即短路，保持 KISS。 |

> 牢记：Middleware 是 SDK 的核心创新，直接决定可观测性与扩展性。任何新特性都应优先考虑是否能通过 middleware 实现，而非改动核心循环。

## Middleware 使用

### 拦截点回顾
`pkg/middleware/types.go` 声明了 `StageBeforeAgent` → `StageAfterAgent` 共 6 个阶段，对应 `middleware.State` 中的共享字段。每个阶段都可能由 `Agent.Run` 调用多次（按迭代数）。

### 示例：日志 + 限流 + 监控
以下示例位于 `examples/middleware/main.go`，展示如何堆叠多个 middleware。所有注释均说明其职责以减少理解成本。

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/cexll/agentsdk-go/pkg/api"
    "github.com/cexll/agentsdk-go/pkg/middleware"
)

func main() {
    windowStart := time.Now()
    var count int
    allow := func() bool {
        if time.Since(windowStart) > time.Second {
            windowStart = time.Now()
            count = 0
        }
        count++
        return count <= 5
    }

    metrics := make(chan string, 32) // 监控事件队列

    logging := middleware.Funcs{
        Identifier: "structured-logger",
        OnBeforeModel: func(ctx context.Context, st *middleware.State) error {
            log.Printf("model> iteration=%d", st.Iteration)
            return nil
        },
        OnAfterTool: func(ctx context.Context, st *middleware.State) error {
            log.Printf("tool> name=%v", st.ToolCall)
            return nil
        },
    }

    throttling := middleware.Funcs{
        Identifier: "limiter",
        OnBeforeAgent: func(ctx context.Context, st *middleware.State) error {
            if !allow() {
                return fmt.Errorf("rate limited: 5 rps")
            }
            return nil
        },
    }

    monitoring := middleware.Funcs{
        Identifier: "metrics",
        OnAfterAgent: func(ctx context.Context, st *middleware.State) error {
            select {
            case metrics <- fmt.Sprintf("latency_ms=%d", st.Values["latency"].(int64)):
            default:
            }
            return nil
        },
        OnBeforeModel: func(ctx context.Context, st *middleware.State) error {
            st.Values["ts"] = time.Now()
            return nil
        },
        OnAfterModel: func(ctx context.Context, st *middleware.State) error {
            if start, ok := st.Values["ts"].(time.Time); ok {
                st.Values["latency"] = time.Since(start).Milliseconds()
            }
            return nil
        },
    }

    chain := middleware.NewChain([]middleware.Middleware{logging, throttling, monitoring})
    runtime, _ := api.New(context.Background(), api.Options{Middleware: chain})
    _ = runtime // 略去运行逻辑，重点在 middleware 组合
}
```

**最佳实践**：
- 将任何全局状态存放在 `middleware.State.Values`，避免直接写入上下文。
- 通过 `middleware.WithTimeout` 控制单个 middleware 执行时间，防止某个监控插件拖垮核心循环。
- 只在必要时访问 `st.ToolResult` 等字段，注意为空时的检查以免 panic。

## 配置文件：`.claude/`

项目默认遵循 Claude Code 的配置约定。一个典型目录如下：

```
.claude/
├── settings.local.json   # 本地调试偏好，已存在
├── config.yaml           # 主配置，声明模型、tools、middleware
└── specs/                # 深入文档（可选）
```

### `settings.local.json`
已存在文件可用于覆盖 CLI/HTTP 示例的默认行为。例如：
```json
{
  "debug": true,
  "default_model": "claude-sonnet-4-5",
  "middleware_timeout_ms": 1500
}
```
- `debug` 控制日志详细度；
- `middleware_timeout_ms` 可映射到 `middleware.WithTimeout`，保持链路稳定。

### `config.yaml`
若缺失可创建此文件，用于声明工具和 middleware：
```yaml
project_root: "."
model:
  provider: "anthropic"
  name: "claude-sonnet-4-5"
middleware:
  - name: "audit-log"
    before_agent: true
  - name: "latency-metrics"
    after_model: true
tools:
  - name: "shell"
    command: "/bin/bash"
```
- CLI/HTTP 示例会优先读取 `.claude/config.yaml`，再合并 flags。
- 每个 middleware 条目最终会转换为 `middleware.Funcs` 或实现 `middleware.Middleware` 的结构体。

## 运行示例

### CLI 模式（`examples/cli`）
```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/cli
# 通过 config 指定 middleware
CLAUDE_CONFIG=../.claude/config.yaml go run . --model claude-sonnet-4-5 --session demo
```
- 首次启动会输出当前注册的工具与 middleware，方便确认链路。
- 可使用 `--stream` 参数启用事件流，用于验证 `middleware.State.Iteration` 是否按预期递增。

### HTTP 模式（`examples/http`）
```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/http
go run . --addr :8080 --config ../../.claude/config.yaml
```
- 健康检查：`curl -s localhost:8080/healthz`。
- 同步请求：
  ```bash
  curl -X POST http://localhost:8080/v1/run        -H 'Content-Type: application/json'        -d '{"prompt":"Summarize middleware stages"}'
  ```
- Streaming：
  ```bash
  curl -N -X POST http://localhost:8080/v1/run/stream        -H 'Content-Type: application/json'        -d '{"prompt":"List tool calls", "session_id":"stream-001"}'
  ```
- 观察日志可验证 `StageBeforeTool` 与 `StageAfterTool` 是否按工具数量触发。

## 常见问题与最佳实践

**Q1: 为什么 `Agent.Run` 提前结束?**
- 检查 `pkg/agent/agent.go` 中的 `ErrMaxIterations`。若 `Options.MaxIterations` 被配置为 0 以外的值，超过次数会返回该错误。
- 确保模型在需要继续调用工具时设置 `ModelOutput.Done=false`。

**Q2: Middleware 中读取 `ToolResult` 时报错?**
- 在 `StageBeforeTool` 阶段尚未生成结果，访问为空会 panic。请在代码中先判断 `st.ToolResult != nil`。

**Q3: 如何共享会话上下文?**
- 使用 `middleware.State.Agent`（类型为 `*agent.Context`）。可以通过类型断言拉取历史，避免重新实现状态机。

**Q4: CLI 与 HTTP 的配置可以复用吗?**
- 可以，将公共内容放入 `.claude/config.yaml`，再通过 CLI flag 或 HTTP 请求体重载差异字段。

**最佳实践清单**
- **KISS**：向 `middleware.Funcs` 添加逻辑前先写注释，确保目的明确。
- **YAGNI**：不要在 `pkg/agent/agent.go` 添加新 Stage，优先在 middleware 里扩展。
- **监控优先**：默认实现已经提供 `middleware.WithTimeout`，生产环境务必开启限制。
- **工具安全**：在 `StageBeforeTool` 内做参数校验，避免危险命令流入 `tool.Execute`。
- **上下文大小**：在 `StageBeforeModel` 实现自动裁剪，保障长对话稳定。

## 下一步
- 阅读 `README.md` 的 **核心特性** 与 **项目结构**，加深对模块边界的理解。
- 深入 `docs/api-reference.md` 学习 `api.Request`/`api.Response` 字段，便于扩展 HTTP API。
- 参考 `docs/architecture.md` 理解 13 个核心包的关系，确保新增功能与架构一致。
- 查看 `examples/mcp`，了解如何通过 `.claude/config.yaml` 注册外部 MCP 服务。
