package telemetry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// StreamUsage holds the last aggregate usage found in a run log or combined
// stdout buffer. Claude/Cursor report usage in "result" JSON lines; Codex can
// report usage through token_count JSONL session events.
type StreamUsage struct {
	SawResult bool
	HasCost   bool
	TotalOnly bool

	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64
	TotalCostUSD    float64
}

// streamResultUsage handles both Claude (snake_case) and Cursor (camelCase) field names.
type streamResultUsage struct {
	// Claude Code format
	InputTokens          int64 `json:"input_tokens"`
	OutputTokens         int64 `json:"output_tokens"`
	CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
	// Cursor format
	InputTokensCC  int64 `json:"inputTokens"`
	OutputTokensCC int64 `json:"outputTokens"`
	CacheReadCC    int64 `json:"cacheReadTokens"`
}

func (u streamResultUsage) input() int64  { return coalesce(u.InputTokens, u.InputTokensCC) }
func (u streamResultUsage) output() int64 { return coalesce(u.OutputTokens, u.OutputTokensCC) }
func (u streamResultUsage) cache() int64  { return coalesce(u.CacheReadInputTokens, u.CacheReadCC) }

func coalesce(a, b int64) int64 {
	if a != 0 {
		return a
	}
	return b
}

type streamResultLine struct {
	Type         string            `json:"type"`
	TotalCostUSD *float64          `json:"total_cost_usd"`
	Usage        streamResultUsage `json:"usage"`
}

type codexTokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

type codexTokenCountLine struct {
	Type    string `json:"type"`
	Payload struct {
		Type string `json:"type"`
		Info struct {
			TotalTokenUsage codexTokenUsage `json:"total_token_usage"`
			LastTokenUsage  codexTokenUsage `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// ParseStreamJSONUsage scans newline-delimited JSON (e.g. Claude/Cursor --output-format stream-json).
// The final aggregate usage line wins (same semantics as task tokens parsing).
func ParseStreamJSONUsage(data []byte) StreamUsage {
	var out StreamUsage
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) < 10 {
			continue
		}
		var rl streamResultLine
		if json.Unmarshal(line, &rl) != nil {
			continue
		}
		if rl.Type == "result" {
			out.SawResult = true
			out.InputTokens = rl.Usage.input()
			out.OutputTokens = rl.Usage.output()
			out.CacheReadTokens = rl.Usage.cache()
			if rl.TotalCostUSD != nil {
				out.TotalCostUSD = *rl.TotalCostUSD
				out.HasCost = true
			}
			continue
		}

		var cl codexTokenCountLine
		if json.Unmarshal(line, &cl) != nil {
			continue
		}
		if cl.Type == "event_msg" && cl.Payload.Type == "token_count" {
			usage := cl.Payload.Info.TotalTokenUsage
			if usage.TotalTokens == 0 {
				usage = cl.Payload.Info.LastTokenUsage
			}
			if usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.CachedInputTokens != 0 || usage.TotalTokens != 0 {
				out.SawResult = true
				out.InputTokens = usage.InputTokens
				out.OutputTokens = usage.OutputTokens
				out.CacheReadTokens = usage.CachedInputTokens
			}
		}
	}
	return out
}

// ParseLogUsage returns the best usage signal available for a run log. It
// prefers exact JSONL usage, then Codex's session file, then Codex's text-only
// "tokens used" footer as a last-resort total.
func ParseLogUsage(data []byte) StreamUsage {
	if u := ParseStreamJSONUsage(data); u.SawResult {
		return u
	}
	if u := ParseCodexSessionUsageFromLog(data); u.SawResult {
		return u
	}
	return ParseCodexTextUsage(data)
}

// ParseCodexTextUsage extracts the plain Codex footer:
//
//	tokens used
//	78,525
//
// This footer is a single total, so it is stored in InputTokens as a fallback
// only. Exact input/cache/output splits should come from Codex JSONL events.
func ParseCodexTextUsage(data []byte) StreamUsage {
	lines := strings.Split(string(data), "\n")
	var out StreamUsage
	for i := 0; i < len(lines); i++ {
		if !strings.EqualFold(strings.TrimSpace(lines[i]), "tokens used") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			raw := strings.TrimSpace(lines[j])
			if raw == "" {
				continue
			}
			n, err := strconv.ParseInt(strings.ReplaceAll(raw, ",", ""), 10, 64)
			if err == nil && n > 0 {
				out.SawResult = true
				out.TotalOnly = true
				out.InputTokens = n
			}
			break
		}
	}
	return out
}

var codexSessionIDRe = regexp.MustCompile(`(?m)^session id:\s*([0-9a-fA-F-]{36})\s*$`)

// CodexSessionIDFromLog returns the Codex session id printed in human-readable
// exec logs. JSON session_meta lines are also supported.
func CodexSessionIDFromLog(data []byte) string {
	if m := codexSessionIDRe.FindSubmatch(data); len(m) == 2 {
		return string(m[1])
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) < 10 || line[0] != '{' {
			continue
		}
		var meta struct {
			Type    string `json:"type"`
			Payload struct {
				ID string `json:"id"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &meta) == nil && meta.Type == "session_meta" && meta.Payload.ID != "" {
			return meta.Payload.ID
		}
	}
	return ""
}

// ParseCodexSessionUsageFromLog loads Codex's persisted JSONL session file and
// extracts the final aggregate token_count event.
func ParseCodexSessionUsageFromLog(data []byte) StreamUsage {
	sessionID := CodexSessionIDFromLog(data)
	if sessionID == "" {
		return StreamUsage{}
	}
	path, ok := FindCodexSessionFile(sessionID)
	if !ok {
		return StreamUsage{}
	}
	sessionData, err := os.ReadFile(path)
	if err != nil {
		return StreamUsage{}
	}
	return ParseStreamJSONUsage(sessionData)
}

// FindCodexSessionFile locates a Codex session JSONL file by id. Codex embeds
// the id in the filename under $CODEX_HOME/sessions/YYYY/MM/DD/.
func FindCodexSessionFile(sessionID string) (string, bool) {
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil || userHome == "" {
			return "", false
		}
		home = filepath.Join(userHome, ".codex")
	}
	pattern := filepath.Join(home, "sessions", "*", "*", "*", "*"+sessionID+".jsonl")
	if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
		return matches[0], true
	}
	return "", false
}

// ModelFromLog extracts the concrete model printed by Codex or embedded in the
// persisted command summary.
func ModelFromLog(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "model:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "model:"))
		}
		if strings.HasPrefix(line, "Command:") {
			fields := strings.Fields(line)
			for i := 0; i < len(fields); i++ {
				if fields[i] == "--model" && i+1 < len(fields) {
					return strings.Trim(fields[i+1], `"'`)
				}
				if strings.HasPrefix(fields[i], "--model=") {
					return strings.Trim(strings.TrimPrefix(fields[i], "--model="), `"'`)
				}
			}
		}
	}
	return ""
}
