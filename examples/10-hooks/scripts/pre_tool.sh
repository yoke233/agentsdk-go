#!/bin/bash
# PreToolUse hook - 工具执行前验证
# 退出码: 0=allow, 1=deny, 2=ask

# 读取 stdin 的 JSON payload
payload=$(cat)

# 提取工具名称
tool_name=$(echo "$payload" | jq -r '.tool_input.name // empty')

echo "[PreToolUse] 工具: $tool_name" >&2

# 示例: 拒绝危险命令
if echo "$payload" | jq -e '.tool_input.params.command' | grep -qE 'rm -rf|dd if='; then
    echo '{"permission": "deny", "reason": "危险命令被拒绝"}' 
    exit 1
fi

# 允许执行
echo '{"permission": "allow"}'
exit 0
