# Hooks 示例

演示 agentsdk-go 的 Shell-based Hooks 功能。Hooks 在 agent 执行工具时**自动触发**，无需手动调用。

## 运行

```bash
export ANTHROPIC_API_KEY=sk-ant-...
chmod +x examples/10-hooks/scripts/*.sh
go run ./examples/10-hooks
```

## 配置方式

### 方式一：代码配置 (TypedHooks)

```go
typedHooks := []hooks.ShellHook{
    {Event: events.PreToolUse, Command: "/path/to/pre_tool.sh"},
    {Event: events.PostToolUse, Command: "/path/to/post_tool.sh"},
}

rt, _ := api.New(ctx, api.Options{
    TypedHooks: typedHooks,
})
```

### 方式二：配置文件 (.claude/settings.json)

```json
{
  "hooks": {
    "PreToolUse": {
      "*": "./scripts/pre_tool.sh"
    },
    "PostToolUse": {
      "*": "./scripts/post_tool.sh"
    }
  }
}
```

## Hook 类型

| Hook | 触发时机 | 退出码含义 |
|------|---------|----------|
| PreToolUse | 工具执行前 | 0=allow, 1=deny, 2=ask |
| PostToolUse | 工具执行后 | 0=success |
| SessionStart | 会话开始 | 0=success |
| SessionEnd | 会话结束 | 0=success |
| SubagentStart | 子 Agent 启动 | 0=success |
| SubagentStop | 子 Agent 停止 | 0=success |

## Payload 格式

Hook 脚本通过 stdin 接收 JSON payload：

```json
{
  "hook_event_name": "PreToolUse",
  "session_id": "hooks-demo",
  "tool_input": {
    "name": "Bash",
    "params": {"command": "pwd"}
  }
}
```

PreToolUse hook 需要输出 JSON 到 stdout：

```json
{"permission": "allow"}
```
