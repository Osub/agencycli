package runner

import (
	"strings"

	"github.com/chenhg5/agencycli/internal/entity"
	"github.com/chenhg5/agencycli/internal/telemetry"
)

// ModelInvoker knows how to invoke a specific agent model CLI.
type ModelInvoker interface {
	// Args returns the command + arguments to invoke the agent.
	// promptFile is a path to a temp file containing the full prompt text.
	// sessionID is the previous session ID (empty = start fresh).
	Args(promptFile, sessionID string) []string

	// UseStdinPrompt reports whether the runner should open promptFile and
	// pipe its contents to the process via stdin instead of referencing the
	// file path in the argument list.
	// When true, the promptFile path is NOT included in Args().
	UseStdinPrompt() bool

	// ParseSessionID attempts to extract a new session ID from the agent's
	// combined stdout output. Returns "" if not found or not supported.
	ParseSessionID(output string) string
}

// InvokerFor returns the ModelInvoker for the given model.
// If the model has a custom runCommand (from AgentMeta), it takes precedence.
// addDirs lists additional directories to expose to the agent (model-specific flags).
// apiModel 是 provider/env 解析后的真实 API 模型名。
// reasoningEffort 会传给支持“推理深度”配置的 CLI，避免后台任务误用用户本机默认值。
func InvokerFor(model entity.AgentModel, runCommand string, addDirs []string, apiModel string, reasoningEffort string) ModelInvoker {
	if runCommand != "" {
		return &customInvoker{tmpl: runCommand}
	}
	switch entity.NormaliseModel(model) {
	case entity.ModelClaudeCode:
		return &claudeInvoker{addDirs: addDirs, apiModel: apiModel}
	case entity.ModelCodex:
		return &codexInvoker{addDirs: addDirs, apiModel: apiModel, reasoningEffort: reasoningEffort}
	case entity.ModelGemini:
		return &geminiInvoker{addDirs: addDirs}
	case entity.ModelOpenCode:
		return &openCodeInvoker{}
	case entity.ModelCursor:
		return &cursorInvoker{}
	default:
		return &genericInvoker{}
	}
}

// ── Claude Code ───────────────────────────────────────────────────────────────

type claudeInvoker struct {
	addDirs  []string
	apiModel string
}

func (c *claudeInvoker) Args(promptFile, sessionID string) []string {
	// Use -p/--print for non-interactive mode; prompt arrives on stdin
	// (UseStdinPrompt returns true so the runner pipes promptFile).
	//
	// --dangerously-skip-permissions is required when running as root inside a
	// Docker sandbox. IS_SANDBOX=1 must also be set (handled by sandbox layer).
	args := []string{
		"claude",
		"-p",
		"--verbose",
		"--output-format", "stream-json",
		"--dangerously-skip-permissions",
	}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	if model := strings.TrimSpace(c.apiModel); model != "" {
		args = append(args, "--model", model)
	}
	for _, dir := range c.addDirs {
		args = append(args, "--add-dir", dir)
	}
	return args
}

func (c *claudeInvoker) UseStdinPrompt() bool { return true }

func (c *claudeInvoker) ParseSessionID(output string) string {
	// Claude emits stream-json lines. First system line contains session_id.
	// Example: {"type":"system","subtype":"init","session_id":"abc123",...}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, `"session_id"`) && strings.Contains(line, `"type":"system"`) {
			// Simple extraction without full JSON parse to avoid import.
			const key = `"session_id":"`
			idx := strings.Index(line, key)
			if idx < 0 {
				continue
			}
			rest := line[idx+len(key):]
			end := strings.Index(rest, `"`)
			if end > 0 {
				return rest[:end]
			}
		}
	}
	return ""
}

// ── Codex ─────────────────────────────────────────────────────────────────────

type codexInvoker struct {
	addDirs         []string
	apiModel        string
	reasoningEffort string
}

func (c *codexInvoker) Args(promptFile, sessionID string) []string {
	// `codex exec -` reads the prompt from stdin.
	// --skip-git-repo-check allows running outside a git repo (agent workspace
	// dirs are not git repos themselves; the project repo is mounted separately).
	// When sessionID is provided, use `codex exec resume` to continue a session.
	//
	// provider.model 解析出来后必须显式传给 Codex CLI。Codex CLI 不会稳定地读取
	// OPENAI_MODEL/CODEX_MODEL 环境变量；如果不加 --model，它会回落到用户本机
	// config.toml 的默认模型，导致 Agency 中配置的 provider.model 被绕过。
	//
	// reasoning effort 也用 CLI config 显式覆盖。自动调度任务通常需要稳定产出，
	// 不适合继承用户本机 xhigh 这类高成本设置；否则 PM 盘点大项目时很容易把
	// token 和上游通道耗尽，表现为 streaming 重试或 503 distributor 不可用。
	var args []string
	if sessionID != "" {
		args = []string{"codex", "exec", "resume"}
		args = c.appendModelArg(args)
		args = c.appendReasoningEffortConfig(args)
		args = append(args, sessionID)
	} else {
		args = []string{"codex", "exec", "--skip-git-repo-check", "--sandbox", "workspace-write"}
		args = c.appendModelArg(args)
		args = c.appendReasoningEffortConfig(args)
	}
	for _, dir := range c.addDirs {
		args = append(args, "--add-dir", dir)
	}
	args = append(args, "-")
	return args
}

func (c *codexInvoker) appendModelArg(args []string) []string {
	model := strings.TrimSpace(c.apiModel)
	if model == "" {
		return args
	}
	return append(args, "--model", model)
}

func (c *codexInvoker) appendReasoningEffortConfig(args []string) []string {
	effort := strings.TrimSpace(c.reasoningEffort)
	if effort == "" {
		return args
	}
	return append(args, "--config", `model_reasoning_effort="`+effort+`"`)
}

func (c *codexInvoker) UseStdinPrompt() bool { return true }

func (c *codexInvoker) ParseSessionID(output string) string {
	return telemetry.CodexSessionIDFromLog([]byte(output))
}

// ── Gemini ────────────────────────────────────────────────────────────────────

type geminiInvoker struct {
	addDirs []string
}

func (g *geminiInvoker) Args(promptFile, sessionID string) []string {
	// -p requires a string value to activate non-interactive/headless mode.
	// Passing "" means headless mode is active; the real prompt arrives via
	// stdin. Gemini appends the -p value to stdin input, so "" = stdin only.
	// --output-format stream-json gives structured output for session parsing.
	args := []string{"gemini", "-y", "--output-format", "stream-json", "-p", ""}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	for _, dir := range g.addDirs {
		args = append(args, "--include-directories", dir)
	}
	return args
}

func (g *geminiInvoker) UseStdinPrompt() bool { return true }

func (g *geminiInvoker) ParseSessionID(_ string) string { return "" }

// ── OpenCode ──────────────────────────────────────────────────────────────────

type openCodeInvoker struct{}

func (o *openCodeInvoker) Args(promptFile, _ string) []string {
	// opencode run reads prompt from stdin when no positional arg is given.
	return []string{"opencode", "run"}
}

func (o *openCodeInvoker) UseStdinPrompt() bool { return true }

func (o *openCodeInvoker) ParseSessionID(_ string) string { return "" }

// ── Cursor ────────────────────────────────────────────────────────────────────
// Cursor CLI is the `agent` binary installed via `curl https://cursor.com/install | bash`.
// Auth: credentials cached in ~/.cursor/ after `agent login`.

type cursorInvoker struct{}

func (c *cursorInvoker) Args(promptFile, sessionID string) []string {
	args := []string{
		"agent",
		"--print",
		"--output-format", "stream-json",
		"--force",
		"--trust",
	}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	return args
}

func (c *cursorInvoker) UseStdinPrompt() bool { return true }

func (c *cursorInvoker) ParseSessionID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, `"session_id"`) {
			const key = `"session_id":"`
			idx := strings.Index(line, key)
			if idx < 0 {
				continue
			}
			rest := line[idx+len(key):]
			if end := strings.Index(rest, `"`); end > 0 {
				return rest[:end]
			}
		}
	}
	return ""
}

// ── Generic ───────────────────────────────────────────────────────────────────

// genericInvoker falls back to `cat` so the prompt text is printed to stdout.
// Users should set run_command in .agencycli-agent.yaml for custom agents.
type genericInvoker struct{}

func (g *genericInvoker) Args(promptFile, _ string) []string {
	return []string{"cat", promptFile}
}

func (g *genericInvoker) UseStdinPrompt() bool { return false }

func (g *genericInvoker) ParseSessionID(_ string) string { return "" }

// ── Custom template invoker ───────────────────────────────────────────────────

// customInvoker uses a shell template from the agent's run_command field.
// Supported placeholders: {prompt_file}, {session_id}
type customInvoker struct {
	tmpl string
}

func (c *customInvoker) Args(promptFile, sessionID string) []string {
	cmd := strings.ReplaceAll(c.tmpl, "{prompt_file}", promptFile)
	cmd = strings.ReplaceAll(cmd, "{session_id}", sessionID)
	return []string{"sh", "-c", cmd}
}

func (c *customInvoker) UseStdinPrompt() bool { return false }

func (c *customInvoker) ParseSessionID(_ string) string { return "" }
