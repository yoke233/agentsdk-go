# agentsdk-go

**全新设计的纯 Claude Code 架构 Go Agent SDK**

这是一个从零打造、生产就绪的 Go Agent SDK，完整实现了 Claude Code 的 7 大核心功能，并新增 **6 个 middleware 拦截点**。架构对标 Claude Code，但以 Go 实现，并把 middleware 系统作为 Claude Code 原版所没有的核心创新。采用配置驱动架构，支持 CLI、CI/CD 和企业平台等多种使用场景。

## 核心特性

### ✨ 新增：6 个 Middleware 拦截点

完整的请求/响应拦截链，支持在每个关键节点注入自定义逻辑：

```
User Request
     ↓
[before_agent]  ← 请求验证、限流、审计日志
     ↓
Agent Loop
     ↓
[before_model]  ← Prompt 增强、上下文裁剪
     ↓
Model.Generate
     ↓
[after_model]   ← 结果过滤、安全检查
     ↓
[before_tool]   ← 工具调用前验证、参数检查
     ↓
Tool.Execute
     ↓
[after_tool]    ← 结果后处理、错误处理
     ↓
[after_agent]   ← 响应格式化、度量上报
     ↓
User Response
```

### ✅ 完整功能覆盖（7/7 Claude Code 核心功能）

- **Hooks**：7 类生命周期事件（PreToolUse, PostToolUse, UserPromptSubmit, SessionStart, Stop, SubagentStop, Notification）
- **MCP（Model Context Protocol）**：客户端支持，会话缓存，重试机制
- **Sandbox**：文件系统和网络隔离，路径白名单，资源限额
- **Skills**：声明式注册，自动激活匹配
- **Subagents**：独立上下文，工具白名单
- **Commands**：斜杠命令解析与执行
- **Plugins**：打包分发，签名验证

### ✅ 高质量实现

- **极简核心**：Agent 核心循环 <300 行，KISS 原则
- **纯架构**：全新设计，采用纯 Claude Code 架构设计
- **测试覆盖率**：新模块平均 **91.1%**，核心模块 ≥95%
- **配置驱动**：完全兼容 `.claude/` 目录结构
- **模块化设计**：13 个独立包，职责清晰
- **无迭代限制**：Agent 循环次数可配置，支持超时保护

## 架构亮点

### 纯 Claude Code 架构

```
┌──────────────────────────────────┐
│   新核心层（6 个模块）           │
│   agent, model, tool, message    │
│   middleware, api                │
└──────────┬───────────────────────┘
           │
┌──────────▼───────────────────────┐
│   Claude Code 7 大核心功能       │
│   config, plugins, core, sandbox │
│   mcp, runtime, security         │
└──────────────────────────────────┘
```

**无任何旧模块依赖**，核心代码仅 ~6k 行，保持易读可维护。

## 项目结构

```
agentsdk-go/
├── pkg/                        # 核心包
│   ├── agent/                  # Agent 核心循环（<300 行）⭐ 新实现
│   ├── middleware/             # 6 个拦截点系统 ⭐ 新增
│   ├── model/                  # Anthropic 模型适配器 ⭐ 新实现
│   ├── tool/                   # 工具注册与执行 ⭐ 新实现
│   ├── message/                # 消息历史管理（内存）⭐ 新增
│   ├── api/                    # 统一 SDK 接口 ⭐ 新实现
│   │
│   ├── config/                 # 配置加载与热更新
│   ├── plugins/                # 插件系统（签名验证）
│   ├── core/
│   │   ├── events/             # 事件总线
│   │   └── hooks/              # Hooks 执行器
│   ├── sandbox/                # 沙箱隔离
│   ├── mcp/                    # MCP 客户端
│   ├── runtime/
│   │   ├── skills/             # Skills 管理
│   │   ├── subagents/          # Subagents 管理
│   │   └── commands/           # Commands 解析
│   └── security/               # 安全工具
│
├── cmd/cli/                    # CLI 入口
├── examples/                   # 核心示例
│   ├── cli/                    # CLI 示例
│   ├── http/                   # HTTP 服务器示例
│   ├── mcp/                    # MCP 客户端示例
│   └── middleware/             # Middleware 完整示例 ⭐ 新增
├── test/integration/           # 集成测试
├── tests/                      # 单元测试、基准测试
└── .claude/specs/              # 开发文档
    └── claude-code-rewrite/
        ├── dev-plan.md         # 开发计划
        └── COMPLETION_REPORT.md # 完成报告
```

## 快速开始

### 安装

```bash
go get github.com/cexll/agentsdk-go
```

### 基础使用

```go
package main

import (
    "context"
    "log"

    "github.com/cexll/agentsdk-go/pkg/api"
    "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
    // 创建 Anthropic 模型提供者
    provider := model.NewAnthropicProvider(
        model.WithAPIKey("your-api-key"),
        model.WithModel("claude-sonnet-4-5"),
    )

    // 创建 Agent
    runtime, err := api.New(
        context.Background(),
        api.Options{
            ProjectRoot:   ".",
            ModelProvider: provider,
        },
    )
    if err != nil {
        log.Fatal(err)
    }
    defer runtime.Close()

    // 运行对话
    result, err := runtime.Run(context.Background(), "Hello, Claude!")
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Response: %s", result.Output)
}
```

### 使用 Middleware

```go
import (
    "github.com/cexll/agentsdk-go/pkg/middleware"
)

// 自定义 middleware
loggingMiddleware := middleware.Middleware{
    BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
        log.Printf("收到请求: %s", req.Input)
        return req, nil
    },
    AfterAgent: func(ctx context.Context, resp *middleware.AgentResponse) (*middleware.AgentResponse, error) {
        log.Printf("返回响应: %s", resp.Output)
        return resp, nil
    },
}

// 创建 Agent 时注入 middleware
runtime, err := api.New(
    context.Background(),
    api.Options{
        ProjectRoot:   ".",
        ModelProvider: provider,
        Middleware:    []middleware.Middleware{loggingMiddleware},
    },
)
```

**💡 查看完整示例**：[examples/middleware](examples/middleware/) 提供了日志、限流、安全检查、监控等完整的 middleware 实现，展示所有 6 个拦截点的实际应用。

### 体验 Middleware 示例

运行完整的 middleware 示例，体验日志记录、限流、安全检查、监控指标等功能：

```bash
cd examples/middleware
export ANTHROPIC_API_KEY=your-api-key
go run .
```

该示例展示了：
- ✅ 6 个拦截点的完整集成
- ✅ 日志记录与请求追踪
- ✅ Token bucket 限流
- ✅ 敏感词过滤与安全检查
- ✅ 延迟监控与错误统计

详见 [examples/middleware/README.md](examples/middleware/README.md) 获取完整说明。

### CLI 使用

```bash
cd cmd/cli
go run main.go --api-key=your-key --model=claude-sonnet-4-5
```

## HTTP API 使用

agentsdk-go 提供了开箱即用的 HTTP API，支持 Anthropic 兼容的 SSE（Server-Sent Events）流式进度推送。

### HTTP 服务器示例

参见 `examples/http` 目录，启动方法：

```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/http
go run .
```

服务器默认监听 `:8080`，提供以下端点：

- `POST /v1/run` - 阻塞式请求，等待完整响应
- `POST /v1/run/stream` - SSE 流式推送，实时进度反馈
- `POST /v1/tools/execute` - 直接执行工具调用

### SSE 流式进度推送

`/v1/run/stream` 端点实现了完整的 Anthropic 兼容 SSE 事件流，提供实时进度反馈：

```bash
curl -N -X POST http://localhost:8080/v1/run/stream \
  -H 'Content-Type: application/json' \
  -d '{"prompt": "列出当前目录", "session_id": "demo"}'
```

**事件流序列**（Anthropic 兼容）：

```
event: agent_start
data: {"type":"agent_start","session_id":"demo"}

event: iteration_start
data: {"type":"iteration_start","iteration":0}

event: message_start
data: {"type":"message_start","message":{...}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"我"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"来"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: tool_execution_start
data: {"type":"tool_execution_start","tool_use_id":"toolu_123","name":"bash_execute"}

event: tool_execution_stop
data: {"type":"tool_execution_stop","tool_use_id":"toolu_123","output":"file1.txt\nfile2.txt"}

event: iteration_stop
data: {"type":"iteration_stop","iteration":0}

event: agent_stop
data: {"type":"agent_stop","total_iterations":1}

event: message_stop
data: {"type":"message_stop","message":{...}}
```

**核心特性**：

1. **Anthropic 完全兼容**：事件结构 100% 兼容 Anthropic Messages API
2. **逐字符流式输出**：`content_block_delta` 事件逐字符推送 LLM 生成的文本
3. **工具执行进度**：`tool_execution_start/stop` 事件实时反馈工具调用
4. **6 个拦截点**：基于 Progress Middleware 实现，可扩展自定义事件
5. **心跳保活**：每 15 秒发送 `ping` 事件防止连接断开

详见 [examples/http/README.md](examples/http/README.md) 获取完整文档。

## 配置

项目使用 `.claude/` 目录结构进行配置（兼容 Claude Code）：

```
.claude/
├── config.yaml       # 项目配置
├── skills/           # Skills 定义
├── commands/         # Commands 定义
├── agents/           # Subagents 定义
└── plugins/          # Plugins 目录
```

配置示例（`config.yaml`）：

```yaml
version: "1.0"
model: "claude-sonnet-4-5"
sandbox:
  enabled: true
  allowed_paths: ["/tmp", "./workspace"]
  network_allow: ["*.example.com"]
mcp:
  servers:
    - name: "my-mcp-server"
      command: "node"
      args: ["server.js"]
```

## 测试

```bash
# 运行所有测试
go test ./...

# 运行核心模块测试
go test ./pkg/agent/... ./pkg/middleware/... ./pkg/model/...

# 运行集成测试
go test ./test/integration/...

# 生成覆盖率报告
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 测试覆盖率

### 新核心模块（全新设计）

| 模块 | 覆盖率 | 状态 |
|------|--------|------|
| **pkg/agent** | **98.3%** | ✅ 超标 |
| **pkg/middleware** | **95.9%** | ✅ 超标 |
| **pkg/model** | **91.4%** | ✅ 达标 |
| **pkg/message** | **87.8%** | ⚠️ 接近 |
| **pkg/api** | **87.2%** | ⚠️ 接近 |
| **pkg/tool** | **85.9%** | ⚠️ 接近 |
| **平均覆盖率** | **91.1%** | ✅ **达标** |

### Claude Code 7 大功能（已实现）

| 模块 | 覆盖率 | 状态 |
|------|--------|------|
| pkg/config | 83.7% | ✅ 功能完备 |
| pkg/plugins | 91.0% | ✅ 达标 |
| pkg/core/events | 92.5% | ✅ 达标 |
| pkg/core/hooks | 91.8% | ✅ 达标 |
| pkg/sandbox | 90.5% | ✅ 达标 |
| pkg/mcp | 90.6% | ✅ 达标 |
| pkg/runtime/skills | 91.5% | ✅ 达标 |
| pkg/runtime/subagents | 91.7% | ✅ 达标 |
| pkg/runtime/commands | 91.4% | ✅ 达标 |
| pkg/security | 保留 | ✅ 使用中 |

## 文档

- [开发计划](.claude/specs/claude-code-rewrite/dev-plan.md) - 全新架构设计计划
- [完成报告](.claude/specs/claude-code-rewrite/COMPLETION_REPORT.md) - 首版发布报告
- [原完成报告](.claude/specs/agentsdk-go-rewrite/COMPLETION_REPORT.md) - 历史版本

## 架构设计原则

### 1. KISS 原则

- Agent 核心循环 <300 行
- 功能通过 middleware 扩展
- 配置优于代码

### 2. 配置驱动

所有功能通过 `.claude/` 配置目录控制，无需修改代码即可扩展功能。

### 3. 事件驱动

使用事件总线处理所有异步操作，支持 7 类 Hook 事件。

### 4. 模块化

13 个独立包，每个包职责单一，接口清晰，易于测试和维护。

### 5. 可插拔 Middleware

6 个拦截点覆盖完整请求/响应生命周期，支持日志、监控、限流、安全检查等场景。

## Middleware 使用场景

### 1. 日志记录

```go
LoggingMiddleware := middleware.Middleware{
    BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
        log.Printf("[REQUEST] %s", req.Input)
        req.Meta["start_time"] = time.Now()
        return req, nil
    },
    AfterAgent: func(ctx context.Context, resp *middleware.AgentResponse) (*middleware.AgentResponse, error) {
        duration := time.Since(resp.Meta["start_time"].(time.Time))
        log.Printf("[RESPONSE] %s (took %v)", resp.Output, duration)
        return resp, nil
    },
}
```

### 2. 限流

```go
RateLimitMiddleware := middleware.Middleware{
    BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
        if !rateLimiter.Allow() {
            return nil, errors.New("rate limit exceeded")
        }
        return req, nil
    },
}
```

### 3. 工具调用监控

```go
ToolMonitorMiddleware := middleware.Middleware{
    BeforeTool: func(ctx context.Context, call *middleware.ToolCall) (*middleware.ToolCall, error) {
        metrics.RecordToolCall(call.Name)
        return call, nil
    },
    AfterTool: func(ctx context.Context, result *middleware.ToolResult) (*middleware.ToolResult, error) {
        if result.Error != nil {
            metrics.RecordToolError(result.ID)
        }
        return result, nil
    },
}
```

## 与 Claude Code 的对比

| 维度 | agentsdk-go | Claude Code |
|------|-------------|-------------|
| **核心功能** | 7/7（100%）| 7/7（100%）|
| **Middleware** | 6 个拦截点 | 无 |
| **配置结构** | .claude/ | .claude/ |
| **Hooks** | 7 类事件 | 7 类事件 |
| **测试覆盖** | 91.1% | 未知 |
| **语言** | Go | TypeScript |
| **代码量** | ~6k LOC | 未知 |
| **架构** | 对标 Claude Code + middleware 增强 | 原生 |

## 技术栈

- **Go 1.24**
- **anthropic-sdk-go** - 官方 Anthropic SDK
- **fsnotify** - 配置热加载
- **测试框架** - 标准 testing 包

## 贡献

欢迎提交 Issue 和 Pull Request。

## 许可证

详见 [LICENSE](LICENSE) 文件。

## 致谢

本项目参考了 Claude Code 的设计理念，使用 Go 语言重新实现了完整的功能集，并新增了 6 个 middleware 拦截点以增强可扩展性。

---

**首版发布于 2025-11-18** | [查看发布报告](.claude/specs/claude-code-rewrite/COMPLETION_REPORT.md)
