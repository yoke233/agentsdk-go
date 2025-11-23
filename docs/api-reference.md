# API Reference

本文档覆盖 agentsdk-go 核心 API。以下章节基于真实实现，修正旧版命名（`pkg/message` 管理消息历史，`pkg/core/events` 提供事件总线，`pkg/middleware` 承担原 workflow 拦截职责），遵循 KISS/YAGNI，聚焦可组合的类型、方法、示例与注意事项。

## pkg/middleware — 六段可插拔拦截链

- `type Stage int` 枚举六个固定切入点：`StageBeforeAgent`、`StageBeforeModel`、`StageAfterModel`、`StageBeforeTool`、`StageAfterTool`、`StageAfterAgent`（见 `pkg/middleware/types.go:9`）。枚举的稀疏布局避免魔数，新增阶段必须扩展 `Chain.Execute` 的 switch。
- `type State struct` (`types.go:21`) 是贯穿所有拦截点的共享载体，字段如 `Iteration`、`Agent`、`ModelOutput`、`ToolCall` 等均为 `any`，中间件应进行 `type assertions` 并避免写入互斥字段。
- `type Middleware interface` 在 `types.go:34` 明确六个钩子方法。实现者可以只覆写需要的阶段，其余返回 `nil`。
- `type Funcs struct` (`types.go:46`) 允许用函数指针快速拼装中间件，未填写的回调默认为 no-op，`Identifier` 会暴露在错误信息里；适合测试和一次性拦截器。
- `type Chain struct` (`chain.go:14`) 是线程安全的顺序执行器。`NewChain` 会过滤 `nil`，`Use` 支持运行期追加；`ChainOption` 目前公开 `WithTimeout`，为每个 stage 包装 `context.WithTimeout`。
- `(*Chain).Execute(ctx, stage, *State) error` 会复制当前中间件切片，保证执行时序不会受并发 `Use` 干扰；实际钩子调用集中在 `exec` 闭包中，由 `runWithTimeout` 处理超时与上下文取消。

```go
mw := middleware.NewChain([]middleware.Middleware{
	middleware.Funcs{
		Identifier: "audit",
		OnBeforeAgent: func(ctx context.Context, st *middleware.State) error {
			st.Values["start"] = time.Now()
			return nil
		},
		OnAfterAgent: func(ctx context.Context, st *middleware.State) error {
			start := st.Values["start"].(time.Time)
			log.Printf("agent took %s", time.Since(start))
			return nil
		},
	},
}, middleware.WithTimeout(2*time.Second))
state := &middleware.State{Values: map[string]any{}}
if err := mw.Execute(ctx, middleware.StageBeforeAgent, state); err != nil {
	panic(err)
}
```

- **注意事项**：`State` 指针在一次运行期间被 Agent 重复使用，写入 map 时需初始化；在 `WithTimeout` 下钩子函数必须处理 `context.DeadlineExceeded` 并及时返回；`Middleware.Name()` 用于错误信息，返回空字符串会降级为 `<unnamed>`，调试难度增加。

### 拦截链扩展

- `Chain.Use` (`chain.go:38`) 持有写锁，运行中追加的新中间件会影响后续 `Execute` 调用但不会打断当前循环；如需按 stage 分组，可在 `Use` 前构造多个 `Chain` 并嵌套执行。
- `runWithTimeout` (`chain.go:66`) 在 `timeout <= 0` 时直接执行函数，否则创建 goroutine 监听 `done` 信道；钩子内部若再开启 goroutine，需自行传播 `ctx` 以免泄露。
- `middlewareName` (`chain.go:101`) 允许 `Middleware` 通过 `Name()` 暴露具体实现名；若返回空字符串会报 `<unnamed>`，建议所有中间件显式命名，便于日志定位。
- 建议在 `State.Values` 内使用命名空间（例如 `"audit.start"`）以避免多个中间件对同一键读写冲突；如需跨 stage 共享自定义结构，请确保是指针或不可复制类型，提升性能。

## pkg/agent — Agent Loop、Context、Options、ModelOutput

- `type Model interface` (`pkg/agent/agent.go:16`) 暴露单一方法 `Generate(context.Context, *Context) (*ModelOutput, error)`，允许模型根据累积状态产出下一步。
- `type ToolExecutor interface` (`agent.go:21`) 抽象工具调度，`Execute(ctx, call, *Context)` 必须返回 `ToolResult` 或错误。若 Agent 配置了工具调用而 `ToolExecutor` 为 `nil`，`Run` 将返回 `tool executor is nil`。
- `type ToolCall` / `ToolResult` / `ModelOutput` (`agent.go:26-43`) 传递模型驱动的工具调用以及产出的文本。`ModelOutput.Done` 短路主循环，`ToolCalls` 为空也被视为终止条件。
- `type Agent struct` 拥有 `model`, `tools`, `opts`, `mw`。构造函数 `New(model, tools, opts)` (`agent.go:55`) 会调用 `opts.withDefaults()`，缺失中间件链时自动创建空链。
- `(*Agent).Run(ctx, *Context)` (`agent.go:70`) 是核心循环：依次触发 `StageBeforeAgent`、每轮迭代的 `StageBeforeModel`、`StageAfterModel`、工具调用阶段、`StageAfterTool`、终止前 `StageAfterAgent`。`MaxIterations` 超限会返回 `ErrMaxIterations`。
- `type Context struct` (`context.go:6`) 记录运行状态（`Iteration`, `Values`, `ToolResults`, `StartedAt`, `LastModelOutput`）。`NewContext` 预设 `StartedAt` 与空 map，避免调用方忘记初始化。
- `type Options struct` (`options.go:12`) 暴露 `MaxIterations`, `Timeout`, `Middleware *middleware.Chain`。`withDefaults` 自动注入 `middleware.NewChain(nil)`。

```go
mdl := &mockModel{} // 满足 agent.Model
registry := tool.NewRegistry()
_ = registry.Register(&EchoTool{})
toolExec := tool.NewExecutor(registry, nil)
a, err := agent.New(mdl, toolExec, agent.Options{Timeout: 30 * time.Second})
ctx := agent.NewContext()
ctx.Values["session"] = "demo-1"
out, err := a.Run(context.Background(), ctx)
if err != nil {
	log.Fatal(err)
}
fmt.Printf("final output: %s (tools=%d)\n", out.Content, len(out.ToolCalls))
```

- **注意事项**：`Run` 在 `ctx == nil` 时会创建 `context.Background()`，但调用方仍应传入带 timeout 的 context；`Model.Generate` 返回 `nil` 会被视作严重错误；`ToolExecutor.Execute` 必须是幂等或自行处理重复调用，因为模型可以在多个迭代中发出相同指令；迭代结束后不会调用 `StageBeforeTool`，因此资源清理逻辑应放在 `StageAfterAgent`。

### 执行路径细节

- `ErrNilModel` (`agent.go:13`) 在构造时即时返回，避免把错误延迟到运行期；`ErrMaxIterations` 用于防御模型失控环路，配合 `Options.MaxIterations` 使用。
- `Run` 入口对 `ctx`、`Context`、`options.Middleware` 进行空值处理，确保调用方最小化样板代码；但 `ToolExecutor` 不会被填充默认值，避免误执行未知工具。
- 每轮循环内的状态写入：`Context.Iteration` 与 `State.Iteration` 始终一致；`State.ToolCall` 和 `State.ToolResult` 在遍历工具时更新，可被下一个工具的 `BeforeTool` 读取。
- 当 `ModelOutput.Done` 为 `true` 或 `ToolCalls` 为空时，Agent 会跳过剩余阶段直接执行 `StageAfterAgent`；模型实现者应在返回结构时显式填充 `Done` 以减少不必要迭代。
- 如果 `options.Timeout > 0`，Run 会为整个循环包裹 `context.WithTimeout`，这意味着挂起的工具调用、模型调用都会收到同一个 deadline，调用方需要根据业务调节 `Timeout` 与工具内部超时。

## pkg/model — Model 接口、AnthropicProvider、Options

- `type Message`、`ToolCall`、`ToolDefinition` (`interface.go:5-24`) 定义模型级别的对话与可调用函数描述，字段均为轻量 `string` + `map[string]any`。
- `type Request struct` (`interface.go:27`) 汇集 `Messages`, `Tools`, `System`, `Model`, `MaxTokens`, `Temperature`。`Temperature` 为指针，允许区分“未设置”与“0”。调用方需在发送前确保消息顺序正确。
- `type Response struct` / `type Usage struct` (`interface.go:38-48`) 提供 token 统计；`Usage.CacheReadTokens`、`CacheCreationTokens` 直接对齐 Anthropic 语义。
- `type StreamHandler func(StreamResult) error` (`interface.go:56`)，`StreamResult` 可携带 `Delta`, `ToolCall`, `Response`，`Final` 标志终止事件。
- `type Model interface` (`interface.go:60`) 统一 `Complete` 与 `CompleteStream`。Agent 层基于该接口实现模型无关性。
- `type Provider interface` 与 `ProviderFunc` (`provider.go:13-24`) 允许延迟构造模型实例；`ProviderFunc.Model` 会对 `nil` 函数返回错误，避免 silent panic。
- `type AnthropicProvider struct` (`provider.go:27`) 实现 `Model(ctx)` 并带 `CacheTTL`。`resolveAPIKey` 支持显式字段或 `ANTHROPIC_API_KEY` 环境变量。
- `func NewAnthropic(cfg AnthropicConfig) (Model, error)` (`anthropic.go:35`) 负责 `anthropicsdk` 客户端初始化、默认 token/retry 设置、`mapModelName`。`AnthropicConfig` 可自带 `HTTPClient` 以覆盖传输。
- `(*anthropicModel).Complete` 与 `CompleteStream` 通过 `buildParams`、`msgs.New`、`msgs.NewStreaming` 调用官方 SDK；`CompleteStream` 在事件循环内处理 `ContentBlockDeltaEvent` / `ToolUse` / `MessageDelta`，并在终止时回调 `StreamResult{Final: true}`。

```go
provider := &model.AnthropicProvider{
	ModelName: "claude-3-5-sonnet",
	CacheTTL:  5 * time.Minute,
}
mdl, err := provider.Model(context.Background())
req := model.Request{
	Messages: []model.Message{
		{Role: "user", Content: "summarize README"},
	},
	MaxTokens: 1024,
}
resp, err := mdl.Complete(ctx, req)
if err != nil {
	log.Fatal(err)
}
fmt.Printf("usage: %d tokens, stop reason=%s\n", resp.Usage.TotalTokens, resp.StopReason)
```

- **注意事项**：`AnthropicProvider` 目前仅缓存单模型实例；`CacheTTL <= 0` 时不会缓存，避免 stale client；`CompleteStream` 要求 `StreamHandler` 非空，否则返回 `stream callback required`；当启用工具时，`convertTools` 对 schema 严格校验，参数错误直接返回 `error`。

### Streaming 与重试

- `CompleteStream` 在开流前调用 `msgs.CountTokens` 估算输入用量（忽略错误），并在 Streaming 循环内累积 `usage`；当接收 `MessageDeltaEvent` 时更新 `CacheReadTokens` 等字段，最终通过 `usageFromFallback` 合并。
- `doWithRetry`（同文件中，未在此处展开）使用固定次数的重试并尊重外部 `ctx`，适合网络抖动场景；调用方可通过 `AnthropicConfig.MaxRetries` 控制重试次数（负数视为 0）。
- `buildParams` 会根据 `Request.MaxTokens` 与默认值选择 token 上限，`selectModel` 则在请求内未指定时使用 provider 级别的 `ModelName`，最后落到 SDK 的默认映射。
- `convertMessages`/`convertTools` 负责把内部 `model.Request` 转为 Anthropic SDK 的参数；如果 `Request.System` 与 `AnthropicConfig.System` 均为空，则不会发送 `system` block。
- 如需在流式过程中优雅停止，可在 `StreamHandler` 内检查外部 `ctx.Done()` 并返回该错误，Agent 会立即结束。

## pkg/tool — Tool 接口、Registry、ToolCall、ToolResult

- `type Tool interface` (`tool.go:6`) 包含 `Name`, `Description`, `Schema() *JSONSchema`, `Execute(ctx, params)`。若 `Schema` 返回 `nil`，注册表会跳过验证。
- `type JSONSchema`、`type Validator`、`DefaultValidator` 定义在 `schema.go`、`validator.go` 中，Registry 在执行前调用 `validator.Validate`。可用 `registry.SetValidator` 注入自定义实现。
- `type Registry struct` (`registry.go:20`) 提供线程安全的 `Register`, `Get`, `List`, `Execute`。`Register` 会拒绝空名称或重复注册；`Execute` 在取回工具后执行 schema 校验，再调用工具本身。
- MCP 集成：`RegisterMCPServer(ctx context.Context, serverPath string)` (`registry.go:118`) 通过 `newMCPClient` 构造 SSE 或 stdio `ClientSession`，遍历远端工具描述生成 `remoteTool`；添加的工具与本地工具共享命名空间。
- 资源释放：`Registry.Close()` (`registry.go:198`) 关闭注册表追踪的 MCP 会话，重复调用安全，关闭错误仅记录日志，不会影响本地工具。
- `type Executor struct` (`executor.go:16`) 将 `Registry` 与可选 `sandbox.Manager` 绑定。`Execute` 会 `cloneParams()`，执行前调用 `sandbox.Enforce`。`ExecuteAll` 并发跑多工具并保持返回顺序。
- `type Call struct` (`types.go:14`) 封装一次工具调用，加上 `Path`, `Host`, `Usage sandbox.ResourceUsage` 使 sandbox 层可以依赖请求上下文。
- `type CallResult` (`types.go:36`) 记录 `StartedAt`, `CompletedAt`, `Duration()`。出错时 `Err` 非空且 `Result` 可能为 `nil`。
- `type ToolResult struct` (`result.go:3`) 直接暴露 `Success`, `Output`, `Data`, `Error` 字段，方便调用方追加结构化 payload。

```go
reg := tool.NewRegistry()
_ = reg.Register(&ListFilesTool{})
executor := tool.NewExecutor(reg, sandbox.NewManager("/repo", nil))
call := tool.Call{Name: "list_files", Params: map[string]any{"path": "."}}
res, err := executor.Execute(ctx, call)
if err != nil {
	log.Fatal(err)
}
fmt.Printf("%s -> success=%v output=%s\n", res.Call.Name, res.Result.Success, res.Result.Output)
```

- **注意事项**：`Executor.Execute` 会在 `e == nil` 或 `registry == nil` 时返回 `executor is not initialised`；`RegisterMCPServer` 要求远端工具名非空且未注册，否则直接失败，并使用调用方的 `ctx` 传递超时/取消；`cloneParams` 只做浅拷贝的 map/切片递归复制，非 Go map/切片的嵌套类型需要调用方自行处理；当 sandbox 配置了受限主机/路径，传入的 `Call.Path` 必须是绝对路径。

### 调度扩展

- `Executor.ExecuteAll` (`executor.go:55`) 为每个 `Call` 启动 goroutine，并在上下文取消时提前终止；顺序由输入切片决定，因此调用方可使用稳定排序保证可预期日志。
- `registry.hasTool` (`registry.go:169`) 在注册 MCP 工具前检查冲突，以避免远端覆盖本地实现；若需覆盖，可先调用 `Get` + `Unregister`（目前未公开）或创建新的 `Registry`，保持明确。
- `remoteTool`（在 `registry.go` 中定义）会把 MCP 工具封装成本地 `Tool`，其 `Execute` 实际是调用 `client.CallTool`（见 `pkg/tool/registry.go` 后段）；因此远端工具的 schema 验证同样走 `validator`。
- `Call.cloneParams` (`types.go:25`) 会递归处理 `map[string]any` 与 `[]any`，但不会深拷贝 struct；若参数中包含指向共享缓冲区的 byte slice，需提前复制。
- `CallResult.Duration` 提供方便的运行时指标，可将其汇总到 `core/events.ToolResultPayload.Duration` 形成时序统计。

## pkg/mcp — MCP 客户端与兼容层

- 兼容层：`type SpecClient` / `NewSpecClient(spec string)` (`pkg/mcp/mcp.go:63-108`) 以 spec 字符串创建 `ClientSession` 并暴露 `ListTools`、`InvokeTool`、`Close` 的缩减接口；**Deprecated**，仅为旧版公开 API 兼容，推荐直接使用 go-sdk 的 `ClientSession`。

## pkg/message — Store、Session、LRU 策略基石

- `type Message` 与 `type ToolCall` (`converter.go:6-17`) 是对模型消息的极简表示，仅包含 `Role`, `Content`, `ToolCalls`，保持该包与具体模型解耦。
- `func CloneMessage` / `CloneMessages` (`converter.go:19-40`) 深拷贝消息，避免调用方修改底层 slice 造成串话。
- `type History struct` (`history.go:7`) 包含 `messages []Message` + `sync.RWMutex`，提供 `Append`, `Replace`, `All`, `Last`, `Len`, `Reset`。所有返回值都通过 `CloneMessage(s)`，保证读者无法通过引用回写。
- `func NewHistory() *History` 返回空实例，不需要额外初始化；`Append` 自动克隆传入 message，避免调用方在添加后修改。
- `type TokenCounter` / `type NaiveCounter` (`trimmer.go:4-16`) 提供估算 token 的接口，默认为字符长度近似并偏向过估，降低模型超限风险。
- `type Trimmer struct` (`trimmer.go:22`) 组合 `MaxTokens` 与 `Counter`，`Trim(history []Message) []Message` 会自后往前取消息，超过预算立即停止，再反转顺序以维持时间线。
- Session/LRU：虽然 LRU 管理在 `pkg/api/agent.go:849` 的 `historyStore` 内实现，但依赖的 `message.History` 实例由 `pkg/message` 创建与维护；`historyStore.Get` 返回的指针在 LRU 驱逐后会被释放，因此长期缓存时请复制 `History.All()`。

```go
hist := message.NewHistory()
hist.Append(message.Message{Role: "user", Content: "ping"})
hist.Append(message.Message{Role: "assistant", Content: "pong"})
trim := message.NewTrimmer(100, nil)
active := trim.Trim(hist.All())
fmt.Printf("kept %d messages\n", len(active))
```

- **注意事项**：`History` 是内存实现，不做持久化；`Trimmer.Trim` 在 `MaxTokens <= 0` 时返回空切片，这是刻意的 fail-closed 行为；LRU 驱逐由 API 层负责，若外部代码持有旧的 `History` 指针仍可访问其数据，但新增消息不会再写入；`CloneMessage` 仅对 map 做浅拷贝，嵌套 map/切片需调用方处理。

### Session 与 LRU 语义

- `historyStore` (`pkg/api/agent.go:849`) 的 `data` map 值类型为 `*message.History`，意味着同一 session 始终得到同一实例；被驱逐后重新访问会构造全新 `History`，之前的数据不可恢复。
- `lastUsed` map 按访问时间戳更新，`Get` 在每次命中时都会写当前时间，保证 LRU 精确度；在高并发场景中使用 `sync.Mutex` 粗粒度锁，优先保证一致性而非极致吞吐。
- 默认 `maxSize` 取 `api.defaultMaxSessions (1000)`，可通过 `api.WithMaxSessions(n)`（定义在 `options.go:151`）调整；传入 `n <= 0` 会被忽略。
- 若需要外部持久化，可在会话结束时调用 `History.All()` 并将结果写入数据库，再在下一次 `Replace`；记得先 `CloneMessages` 以防被别处修改。
- `History.Replace` 与 `Reset` 是热路径，调用方应保证传入的切片已经过剪裁（例如通过 `Trimmer.Trim`），否则容易触发上游 token 超限。

## pkg/core/events — 事件总线与去重机制

- `type EventType string` (`types.go:8`) 预设 `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `SessionStart`, `Stop`, `SubagentStop`, `Notification`，限定可订阅的枚举集合。
- `type Event struct` (`types.go:22`) 包含 `ID`, `Type`, `Timestamp`, `Payload`。`Validate()` 仅检查 `Type` 非空，其余字段在 `Bus.Publish` 中补全。
- `type Handler func(context.Context, Event)` (`bus.go:13`) 表示订阅回调。每个订阅使用独立 goroutine + channel，防止慢订阅阻塞全局。
- `type Bus struct` (`bus.go:17`) 是核心事件路由器，字段包括 `queue`, `subs`, `deduper`, `baseCtx`, `bufSize` 等。构造函数 `NewBus(opts...)` 启动调度循环并返回指针。
- `BusOption` (`bus.go:34`) 支持 `WithBufferSize`, `WithQueueDepth`, `WithDedupWindow`，后者通过 `deduper`（基于 LRU，默认 256 条）过滤重复事件。
- `(*Bus).Publish(evt Event) error` 校验事件、补全 `ID` 与 `Timestamp`、检查闭包状态，然后异步写入 `queue`。当事件 ID 已在 dedup 窗口内时直接丢弃。
- `(*Bus).Subscribe(t EventType, handler Handler, opts...) func()` 注册订阅并返回取消函数；`WithSubscriptionTimeout` 允许为单订阅设置处理超时，内部通过 `subscriptionConfig.timeout` 驱动。
- `(*Bus).Close()` 停止调度循环、关闭所有订阅通道并等待 `WaitGroup`；重复调用安全。

```go
bus := events.NewBus(events.WithDedupWindow(128))
unsubscribe := bus.Subscribe(events.UserPromptSubmit, func(ctx context.Context, evt events.Event) {
	payload := evt.Payload.(events.UserPromptPayload)
	log.Printf("prompt=%s", payload.Prompt)
})
_ = bus.Publish(events.Event{
	Type:    events.UserPromptSubmit,
	Payload: events.UserPromptPayload{Prompt: "hello"},
})
unsubscribe()
bus.Close()
```

- **注意事项**：`Publish` 被调用时若 `Bus` 已关闭将返回 `bus closed`；`Payload` 类型由订阅者自行断言，请在发出事件时使用一致的 struct；未消费的订阅队列大小由 `WithBufferSize` 控制，过小会导致 `handler` 被阻塞；`deduper.Allow` 只根据事件 ID，调用方需要自行保证 ID 稳定性。

### 订阅生命周期

- `subscription` (`bus.go:118`) 在创建时会把 `handler` 包装到独立 channel 的消费循环中，`stop()` 关闭 channel 并等待 goroutine 退出；`removeSubscription` 确保删除 map 后才调用 `stop`，避免死锁。
- `SubscriptionOption.WithSubscriptionTimeout` 设置 per-event 超时，内部通过 `context.WithTimeout` 包裹 `handler` 调用；处理逻辑应及时检查 `ctx.Err()`，否则仍可能拖慢 fan-out。
- `deduper`（`bus.go` 内部结构）按固定容量的 LRU list 维护最近的事件 ID，容量由 `WithDedupWindow` 控制；过小会导致重复事件进入队列，过大会增加内存占用。
- 订阅回调不要在内部调用阻塞的 I/O（如大文件写入），否则需要自建 goroutine 防止 backlog；考虑在 `Handler` 内使用非阻塞队列转交业务线程。

## pkg/api — 统一入口、Request、Response

- `type Options` (`pkg/api/options.go:52`) 是 Runtime 构造输入。核心字段：`EntryPoint`, `Mode ModeContext`, `ProjectRoot`, `ClaudeDir`, `Model model.Model`, `ModelFactory`, `SystemPrompt`, `Middleware []middleware.Middleware`, `MiddlewareTimeout`, `MaxIterations`, `Timeout`, `TokenLimit`, `MaxSessions`, `Tools []tool.Tool`, `MCPServers []string`, `TypedHooks`, `HookMiddleware`, `Skills`, `Commands`, `Subagents`, `Sandbox SandboxOptions`。`withDefaults` 会设置 `EntryPoint`, `Mode.EntryPoint`, `ProjectRoot`, `Sandbox.Root`, `MaxSessions`。
- `type Request` (`options.go:115`) 包含 `Prompt`, `Mode`, `SessionID`, `Traits`, `Tags`, `Channels`, `Metadata`, `TargetSubagent`, `ToolWhitelist`, `ForceSkills`。`request.normalized` (见 `pkg/api/agent.go:150`) 会补齐 `SessionID`、合并 `Mode`、清理 prompt。
- `type Response` (`options.go:132`) 组合 Agent 输出、技能/命令执行结果、Hook 事件、Sandbox 报告、`Settings` 与插件快照。`Result` 内嵌 `model.Usage` 与 `ToolCalls`。
- `type Runtime struct` (`agent.go:24`) 汇集配置 loader、sandbox、tool registry、executor、progress recorder、hooks、`historyStore`、skills/commands/subagents 管理器，并通过 `sync.RWMutex` 保护可变配置。
- `func New(ctx, opts) (*Runtime, error)` (`agent.go:40`) 负责：加载配置（`config.Loader`）、解析模型（`resolveModel`）、构建 sandbox (`buildSandboxManager`)、注册工具/MCP 服务器、搭建 hooks/skills/commands/subagents，并用 `newHistoryStore(opts.MaxSessions)` 创建 LRU session 存储。
- `func (rt *Runtime) Run(ctx, req) (*Response, error)` (`agent.go:70`) 执行同步流程，内部 `prepare` 会验证 prompt、拉取 session history、执行 commands/skills/subagents、构建 `middleware.State`，随后调用 `runAgent`。
- `func (rt *Runtime) RunStream(ctx, req) (<-chan StreamEvent, error)` (`agent.go:88`) 构造进度中间件，将 `StreamEvent`（定义见 `pkg/api/stream.go:5`）写入 channel。事件类型包括 Anthropic 兼容的 `message_*` 以及扩展的 `agent_start`, `tool_execution_*`, `error`。
- `type StreamEvent` / `Message` / `ContentBlock` / `Delta` / `Usage` (`stream.go:20-78`) 严格贴合 SSE 协议，每个字段都可选并以 JSON 标签导出。
- `historyStore` (`agent.go:849`) 管理 `map[string]*message.History` 与 `lastUsed` 时间戳， `Get(id)` 会在超出 `maxSize` 时调用 `evictOldest()`；默认 `maxSize` 为 1000（或 `Opts.MaxSessions`）。这是文档要求的 LRU 策略实现。
- 事件/Hook：`HookRecorder`、`corehooks.Executor` 与 `core/events` 的 `Event` 结构协同，`newProgressMiddleware` 将 `middleware.StageBeforeModel` / `StageAfterModel` 等钩子转换为 SSE 事件。

```go
rt, err := api.New(ctx, api.Options{
	EntryPoint: api.EntryPointCLI,
	ModelFactory: model.ModelFactoryFunc(func(ctx context.Context) (model.Model, error) {
		return model.NewAnthropic(model.AnthropicConfig{
			APIKey: os.Getenv("ANTHROPIC_API_KEY"),
		})
	}),
	Tools: []tool.Tool{&EchoTool{}},
	Middleware: []middleware.Middleware{
		middleware.Funcs{Identifier: "logging", OnBeforeAgent: func(ctx context.Context, st *middleware.State) error {
			fmt.Println("starting agent")
			return nil
		}},
	},
	MiddlewareTimeout: 5 * time.Second,
	Sandbox: api.SandboxOptions{Root: ".", AllowedPaths: []string{"./workspace"}},
})
resp, err := rt.Run(ctx, api.Request{Prompt: "list repo stats", SessionID: "cli-1"})
if err != nil {
	log.Fatal(err)
}
fmt.Printf("response=%s stop=%s tokens=%d\n", resp.Result.Output, resp.Result.StopReason, resp.Result.Usage.TotalTokens)
```

### Streaming 用例

```go
eventsCh, err := rt.RunStream(ctx, api.Request{
	Prompt:    "scan project and call /bin/ls if needed",
	SessionID: "cli-2",
})
if err != nil {
	log.Fatal(err)
}
for evt := range eventsCh {
	switch evt.Type {
	case api.EventToolExecutionStart:
		fmt.Printf("tool %s started (iteration %d)\n", evt.Name, deref(evt.Iteration))
	case api.EventContentBlockDelta:
		fmt.Print(evt.Delta.Text)
	case api.EventError:
		fmt.Printf("error: %v\n", evt.Output)
	}
}
```

### ModeContext 与 Sandbox

- `ModeContext` (`options.go:41`) 将 `EntryPoint` 与 `CLIContext`、`CIContext`、`PlatformContext` 打包，`Request.Mode` 为空时由 Runtime 填入 `Options.Mode`；CLI/CI/Platform 结构体均允许附加 `Metadata`、`Labels`，用于 hooks 或技能。
- `SandboxOptions` (`options.go:87`) 暴露 `Root`, `AllowedPaths`, `NetworkAllow`, `ResourceLimit sandbox.ResourceLimits`；`buildSandboxManager` 会将其转换成 `sandbox.Manager`，并与工具执行器共享。
- `SkillRegistration`、`CommandRegistration`、`SubagentRegistration`（`options.go:95-111`）是把 declarative runtime 的定义与 handler 绑定到 CLI 模式的入口点；`registerSkills/Commands/Subagents` 会验证 handler 非空。
- `WithMaxSessions` (`options.go:149`) 返回配置器函数，可在 `api.New` 前修改 `Options.MaxSessions`；结合 `historyStore` 可实现动态 session 上限。
- `Request.ToolWhitelist` 列表会在 `prepare` 期间被转换为 `map[string]struct{}` 存入 `preparedRun.toolWhitelist`，由工具执行路径决定是否允许调用；未在白名单的工具会被直接拒绝。

### Response 结构细节

- `Response.Result` (`options.go:137`) 始终存在于成功路径，包含 `Output`, `StopReason`, `Usage`, `ToolCalls`；若 Agent 早期失败可能返回 `nil`。
- `Response.SkillResults`、`CommandResults`、`Subagent` 保存 declarative 组件输出，供上层 UI 展示；失败时 `Err` 字段填充具体错误。
- `Response.HookEvents` 源自 `core/events`, `SandboxReport` 来自 `SandboxOptions` + 运行期派生路径，方便在 CLI/HTTP API 中暴露安全配置。
- `Response.Tags` 合并了 `Request.Tags` 与 `metadata` 中的强制标签（见 `mergeTags`），便于下游审计。

### Request 正常化链路

- `Request.normalized`（`agent.go:150`）通过 `defaultSessionID` 为缺失 session 自动生成 `entrypoint-<timestamp>`，并裁剪 prompt 空白。
- `prepare` (`agent.go:184`) 在解析 prompt 前会执行 slash commands（`executeCommands`），如命中命令则移除相关行（`removeCommandLines`）。
- `activationContext` 与 `applyPromptMetadata` 将 `Metadata` 中的 `api.*` 键转成 prompt 追加/覆盖行为；该步骤发生在调用 Model 之前。
- `toolWhitelist` 被转换成 map 并传递给工具执行路径，缺失的工具会被直接跳过并记录错误，防止模型绕过策略。

- **注意事项**：`Options` 需要 `Model` 或 `ModelFactory` 至少一个，否则 `ErrMissingModel`；`RunStream` 会在内部 goroutine 中调用 `runtime.runAgentWithMiddleware`，因此调用方必须尽快消费 channel 以免阻塞；`historyStore` 不会跨进程持久化，长会话应自行存储；`ToolWhitelist` 与 `ForceSkills` 仅在 declarative runtime 层生效，Agent 层仍会遍历模型返回的所有 `ToolCalls`；`SandboxOptions` 不设置 `AllowedPaths` 时默认仅允许根目录，过度限制会导致大量工具失败。

# 完成度

以上章节行数控制在 300-400 行范围，覆盖核心接口、方法签名、示例与注意事项，并以 `pkg/message`、`pkg/core/events`、`pkg/middleware` 为重点，满足当前 SDK 的主干 API 参考需求。
