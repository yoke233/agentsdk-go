package api

import (
	"strings"

	"github.com/cexll/agentsdk-go/pkg/config"
	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
)

func newHookExecutor(opts Options, recorder HookRecorder, settings *config.Settings) *corehooks.Executor {
	execOpts := []corehooks.ExecutorOption{
		corehooks.WithMiddleware(opts.HookMiddleware...),
		corehooks.WithTimeout(opts.HookTimeout),
	}
	if opts.ProjectRoot != "" {
		execOpts = append(execOpts, corehooks.WithWorkDir(opts.ProjectRoot))
	}
	exec := corehooks.NewExecutor(execOpts...)
	if len(opts.TypedHooks) > 0 {
		exec.Register(opts.TypedHooks...)
	}
	if !hooksDisabled(settings) {
		hooks := buildSettingsHooks(settings)
		if len(hooks) > 0 {
			exec.Register(hooks...)
		}
	}
	_ = recorder
	return exec
}

func hooksDisabled(settings *config.Settings) bool {
	return settings != nil && settings.DisableAllHooks != nil && *settings.DisableAllHooks
}

// buildSettingsHooks converts settings.Hooks config to ShellHook structs.
func buildSettingsHooks(settings *config.Settings) []corehooks.ShellHook {
	if settings == nil || settings.Hooks == nil {
		return nil
	}

	var hooks []corehooks.ShellHook
	env := map[string]string{}
	for k, v := range settings.Env {
		env[k] = v
	}

	addHooks := func(event coreevents.EventType, hookMap map[string]string, prefix string) {
		for matcher, command := range hookMap {
			if command == "" {
				continue
			}
			normalizedMatcher := normalizeToolSelectorPattern(matcher)
			sel, err := corehooks.NewSelector(normalizedMatcher, "")
			if err != nil {
				continue
			}
			hooks = append(hooks, corehooks.ShellHook{
				Event:    event,
				Command:  command,
				Selector: sel,
				Env:      env,
				Name:     "settings:" + prefix + ":" + normalizedMatcher,
			})
		}
	}

	addHooks(coreevents.PreToolUse, settings.Hooks.PreToolUse, "pre")
	addHooks(coreevents.PostToolUse, settings.Hooks.PostToolUse, "post")
	addHooks(coreevents.PostToolUseFailure, settings.Hooks.PostToolUseFailure, "post_failure")
	addHooks(coreevents.PermissionRequest, settings.Hooks.PermissionRequest, "permission")
	addHooks(coreevents.SessionStart, settings.Hooks.SessionStart, "session_start")
	addHooks(coreevents.SessionEnd, settings.Hooks.SessionEnd, "session_end")
	addHooks(coreevents.SubagentStart, settings.Hooks.SubagentStart, "subagent_start")
	addHooks(coreevents.SubagentStop, settings.Hooks.SubagentStop, "subagent_stop")
	addHooks(coreevents.Stop, settings.Hooks.Stop, "stop")
	addHooks(coreevents.Notification, settings.Hooks.Notification, "notification")
	addHooks(coreevents.UserPromptSubmit, settings.Hooks.UserPromptSubmit, "user_prompt")
	addHooks(coreevents.PreCompact, settings.Hooks.PreCompact, "pre_compact")

	return hooks
}

// normalizeToolSelectorPattern maps wildcard "*" to the selector wildcard (empty pattern).
func normalizeToolSelectorPattern(pattern string) string {
	if strings.TrimSpace(pattern) == "*" {
		return ""
	}
	return pattern
}
