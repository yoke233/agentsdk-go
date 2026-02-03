#!/bin/bash
# PostToolUse hook - 工具执行后记录

payload=$(cat)
tool_name=$(echo "$payload" | jq -r '.tool_response.name // empty')
duration_ms=$(echo "$payload" | jq -r '.tool_response.duration_ms // "unknown"')

echo "[PostToolUse] 工具: $tool_name, 耗时: ${duration_ms}ms" >&2
exit 0
