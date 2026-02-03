package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"runtime"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/core/events"
	"github.com/cexll/agentsdk-go/pkg/core/hooks"
	modelpkg "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
	// 获取当前源文件所在目录作为示例根目录
	_, currentFile, _, _ := runtime.Caller(0)
	exampleDir := filepath.Dir(currentFile)
	scriptsDir := filepath.Join(exampleDir, "scripts")

	// 方式一：通过 TypedHooks 代码配置 (推荐用于动态配置)
	typedHooks := []hooks.ShellHook{
		{Event: events.PreToolUse, Command: filepath.Join(scriptsDir, "pre_tool.sh")},
		{Event: events.PostToolUse, Command: filepath.Join(scriptsDir, "post_tool.sh")},
	}

	// 创建 provider
	provider := &modelpkg.AnthropicProvider{
		ModelName: "claude-sonnet-4-5-20250514",
	}

	// 初始化运行时
	// hooks 会在 agent 执行工具时自动触发，无需手动 Publish
	// 方式二：通过 .claude/settings.json 配置 hooks (见 .claude/settings.json)
	rt, err := api.New(context.Background(), api.Options{
		ModelFactory: provider,
		ProjectRoot:  exampleDir, // 设置项目根目录，也可从 .claude/settings.json 加载 hooks
		TypedHooks:   typedHooks,
	})
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	fmt.Println("=== Hooks 示例 ===")
	fmt.Println("已注册 hooks: PreToolUse, PostToolUse")
	fmt.Println("hooks 会在 agent 执行工具时自动触发")
	fmt.Println()

	// 执行 agent 调用 - hooks 会自动触发
	fmt.Println(">>> 执行 Agent 调用")
	fmt.Println("    当 agent 调用 Bash 工具时，PreToolUse 和 PostToolUse hooks 会自动执行")
	fmt.Println()

	resp, err := rt.Run(context.Background(), api.Request{
		Prompt: "请用 pwd 命令显示当前目录",
		// SessionID: "hooks-demo",
	})
	if err != nil {
		log.Printf("run error: %v", err)
	} else if resp.Result != nil {
		fmt.Printf("\n输出: %s\n", resp.Result.Output)
	}

	fmt.Println("\n=== 示例结束 ===")
}
