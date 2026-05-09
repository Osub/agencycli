package telemetry

import (
	"regexp"
	"strings"
)

var (
	modelToolExitRe  = regexp.MustCompile(`(?m)\bexited\s+([1-9][0-9]*)\b`)
	modelToolErrorRe = regexp.MustCompile(`(?mi)^\s*(?:error:|fatal:|zsh:\d+: command not found:|bash:\s*[^:\n]+:\s*command not found|/bin/sh:\s*[^:\n]+:\s*not found)`)
	toolUseErrorRe   = regexp.MustCompile(`(?i)(<tool_use_error>|InputValidationError:|failed due to the following issue:|The required parameter .+ is missing|Invalid input: expected .+, received undefined)`)
	taskDoneOKRe     = regexp.MustCompile(`(?m)^\s*✓ Task .+ marked done_success\s*$`)
	taskDoneFailRe   = regexp.MustCompile(`(?m)^\s*✓ Task .+ marked done_failed\s*$`)
)

// ToolFailure captures a failed shell/tool execution that the model CLI may
// still wrap in an overall zero exit code.
type ToolFailure struct {
	Message string
}

// DetectModelToolFailure scans model run logs for failed tool invocations. It
// deliberately ignores agencycli callback commands, because those are handled by
// task status in the workspace store.
func DetectModelToolFailure(data []byte) (ToolFailure, bool) {
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if code := toolExitCode(line); code != "" {
			if shouldIgnoreToolContext(lines, i) {
				continue
			}
			return ToolFailure{Message: compactFailureContext(lines, i, "tool exited "+code)}, true
		}
		if modelToolErrorRe.MatchString(line) {
			if shouldIgnoreToolContext(lines, i) {
				continue
			}
			return ToolFailure{Message: compactFailureContext(lines, i, strings.TrimSpace(line))}, true
		}
		if toolUseErrorRe.MatchString(line) {
			if shouldIgnoreToolContext(lines, i) {
				continue
			}
			msg := compactFailureContext(lines, i, strings.TrimSpace(line))
			if strings.Contains(line, "InputValidationError") || strings.Contains(line, "required parameter") {
				msg += "\n\nCompatibility hint: the model emitted a Claude Code tool call without the required input fields. If this is using a non-Anthropic model through an Anthropic-compatible gateway, check that the gateway preserves tool_use.input exactly."
			}
			return ToolFailure{Message: msg}, true
		}
	}
	return ToolFailure{}, false
}

// DetectAgencycliTaskTerminalStatus inspects the run log for an explicit
// agencycli task callback. It is used to distinguish "intermediate tool error,
// then recovered and explicitly marked success" from "tool error and no real
// completion signal".
func DetectAgencycliTaskTerminalStatus(data []byte) string {
	switch {
	case taskDoneOKRe.Match(data):
		return "done_success"
	case taskDoneFailRe.Match(data):
		return "done_failed"
	default:
		return ""
	}
}

func toolExitCode(line string) string {
	m := modelToolExitRe.FindStringSubmatch(line)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

func shouldIgnoreToolContext(lines []string, idx int) bool {
	start := idx - 2
	if start < 0 {
		start = 0
	}
	end := idx + 1
	if end >= len(lines) {
		end = len(lines) - 1
	}
	for i := start; i <= end; i++ {
		lower := strings.ToLower(lines[i])
		if strings.Contains(lower, "agencycli task done") ||
			strings.Contains(lower, "agencycli task confirm-request") ||
			strings.Contains(lower, "$agencycli_bin") {
			return true
		}
	}
	return false
}

func compactFailureContext(lines []string, idx int, fallback string) string {
	start := idx
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	end := idx
	for end+1 < len(lines) && strings.TrimSpace(lines[end+1]) != "" {
		end++
		if end-idx >= 3 {
			break
		}
	}
	var picked []string
	for _, line := range lines[start : end+1] {
		line = strings.TrimSpace(line)
		if line != "" {
			picked = append(picked, line)
		}
	}
	if len(picked) == 0 {
		return fallback
	}
	msg := strings.Join(picked, "\n")
	if len(msg) > 800 {
		msg = msg[:800] + "..."
	}
	return msg
}
