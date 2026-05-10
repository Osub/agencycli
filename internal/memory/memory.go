// Package memory retrieves small, optional memory snippets for agent prompts.
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxSnippets = 5
	defaultMaxChars    = 6000
	defaultTimeout     = 3 * time.Second
	defaultQueryChars  = 2000
)

// Query describes the current AgencyCli run that should be used as the memory
// search query.
type Query struct {
	Project   string
	Agent     string
	TaskID    string
	TaskTitle string
	Prompt    string
}

// Result contains the formatted context and non-fatal retrieval diagnostics.
type Result struct {
	Markdown []byte
	Snippets []Snippet
	Warnings []string
}

// Snippet is a normalized memory result from OpenViking or EverCore.
type Snippet struct {
	Source string
	Kind   string
	Title  string
	Text   string
	URI    string
	Score  *float64
}

type config struct {
	enabled     bool
	maxSnippets int
	maxChars    int
	timeout     time.Duration
	queryChars  int

	openVikingURL            string
	openVikingAPIKey         string
	openVikingAccount        string
	openVikingUser           string
	openVikingAgent          string
	openVikingTargetURI      string
	openVikingScoreThreshold *float64

	everCoreURL         string
	everCoreAPIKey      string
	everCoreUserID      string
	everCoreGroupID     string
	everCoreMethod      string
	everCoreMemoryTypes []string
}

type sourceResult struct {
	order    int
	snips    []Snippet
	warnings []string
}

// Retrieve fetches memory context according to AGENCYCLI_MEMORY_* environment
// variables. It never returns an error: memory is supplemental and should not
// block task execution.
func Retrieve(parent context.Context, env []string, q Query) Result {
	cfg := configFromEnv(env)
	if !cfg.enabled {
		return Result{}
	}

	ctx, cancel := context.WithTimeout(parent, cfg.timeout)
	defer cancel()

	query := buildSearchQuery(q, cfg.queryChars)
	if query == "" {
		return Result{}
	}

	client := &http.Client{Timeout: cfg.timeout}
	ch := make(chan sourceResult, 2)
	var wg sync.WaitGroup

	if cfg.openVikingURL != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snips, err := searchOpenViking(ctx, client, cfg, query, q)
			res := sourceResult{order: 0, snips: snips}
			if err != nil {
				res.warnings = append(res.warnings, "openviking: "+err.Error())
			}
			ch <- res
		}()
	}

	if cfg.everCoreURL != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snips, err := searchEverCore(ctx, client, cfg, query)
			res := sourceResult{order: 1, snips: snips}
			if err != nil {
				res.warnings = append(res.warnings, "evercore: "+err.Error())
			}
			ch <- res
		}()
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	var results []sourceResult
	for res := range ch {
		results = append(results, res)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].order < results[j].order })

	var out Result
	for _, res := range results {
		out.Snippets = append(out.Snippets, res.snips...)
		out.Warnings = append(out.Warnings, res.warnings...)
	}
	out.Snippets = dedupeSnippets(out.Snippets)
	out.Markdown = []byte(formatMarkdown(out.Snippets, cfg.maxSnippets, cfg.maxChars))
	return out
}

// Inject appends retrieved memory after the user's prompt. For task prompts the
// runner appends its operational footer after this, keeping AgencyCli metadata
// last.
func Inject(prompt string, result Result) string {
	if len(result.Markdown) == 0 {
		return prompt
	}
	return strings.TrimRight(prompt, "\n") + "\n\n" + string(result.Markdown)
}

func configFromEnv(env []string) config {
	get := envLookup(env)
	cfg := config{
		enabled:             truthy(get("AGENCYCLI_MEMORY_ENABLED")),
		maxSnippets:         intFromEnv(get, defaultMaxSnippets, "AGENCYCLI_MEMORY_MAX_SNIPPETS"),
		maxChars:            intFromEnv(get, defaultMaxChars, "AGENCYCLI_MEMORY_MAX_CHARS"),
		timeout:             durationFromEnv(get("AGENCYCLI_MEMORY_TIMEOUT"), defaultTimeout),
		queryChars:          intFromEnv(get, defaultQueryChars, "AGENCYCLI_MEMORY_QUERY_CHARS"),
		openVikingURL:       firstNonEmpty(get("AGENCYCLI_OPENVIKING_URL"), get("OPENVIKING_URL")),
		openVikingAPIKey:    firstNonEmpty(get("AGENCYCLI_OPENVIKING_API_KEY"), get("OPENVIKING_API_KEY")),
		openVikingAccount:   firstNonEmpty(get("AGENCYCLI_OPENVIKING_ACCOUNT"), "default"),
		openVikingUser:      firstNonEmpty(get("AGENCYCLI_OPENVIKING_USER"), "default"),
		openVikingAgent:     firstNonEmpty(get("AGENCYCLI_OPENVIKING_AGENT"), get("AGENCYCLI_AGENT"), "default"),
		openVikingTargetURI: firstNonEmpty(get("AGENCYCLI_OPENVIKING_TARGET_URI"), "viking://"),
		everCoreURL: firstNonEmpty(
			get("AGENCYCLI_EVERCORE_URL"),
			get("AGENCYCLI_EVERMEMOS_URL"),
			get("EVERCORE_URL"),
			get("EVERMEMOS_URL"),
		),
		everCoreAPIKey:  firstNonEmpty(get("AGENCYCLI_EVERCORE_API_KEY"), get("AGENCYCLI_EVERMEMOS_API_KEY"), get("EVERCORE_API_KEY"), get("EVERMEMOS_API_KEY")),
		everCoreUserID:  firstNonEmpty(get("AGENCYCLI_EVERCORE_USER_ID"), get("AGENCYCLI_MEMORY_USER_ID"), "agencycli"),
		everCoreGroupID: firstNonEmpty(get("AGENCYCLI_EVERCORE_GROUP_ID"), get("AGENCYCLI_MEMORY_GROUP_ID")),
		everCoreMethod:  firstNonEmpty(get("AGENCYCLI_EVERCORE_METHOD"), "keyword"),
	}

	if v := strings.TrimSpace(get("AGENCYCLI_OPENVIKING_SCORE_THRESHOLD")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.openVikingScoreThreshold = &f
		}
	}
	cfg.everCoreMemoryTypes = listFromEnv(
		get("AGENCYCLI_EVERCORE_MEMORY_TYPES"),
		[]string{"episodic_memory", "agent_memory"},
	)
	if cfg.maxSnippets <= 0 {
		cfg.maxSnippets = defaultMaxSnippets
	}
	if cfg.maxChars <= 0 {
		cfg.maxChars = defaultMaxChars
	}
	if cfg.queryChars <= 0 {
		cfg.queryChars = defaultQueryChars
	}
	return cfg
}

func searchOpenViking(ctx context.Context, client *http.Client, cfg config, query string, q Query) ([]Snippet, error) {
	endpoint, err := joinURL(cfg.openVikingURL, "/api/v1/search/find")
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"query":      query,
		"target_uri": cfg.openVikingTargetURI,
		"limit":      cfg.maxSnippets,
	}
	if cfg.openVikingScoreThreshold != nil {
		body["score_threshold"] = *cfg.openVikingScoreThreshold
	}

	var resp struct {
		Status string `json:"status"`
		Result struct {
			Memories  []openVikingItem `json:"memories"`
			Resources []openVikingItem `json:"resources"`
			Skills    []openVikingItem `json:"skills"`
		} `json:"result"`
		Message string `json:"message"`
	}
	headers := map[string]string{
		"X-OpenViking-Account": cfg.openVikingAccount,
		"X-OpenViking-User":    cfg.openVikingUser,
		"X-OpenViking-Agent":   firstNonEmpty(cfg.openVikingAgent, q.Agent, "default"),
	}
	if cfg.openVikingAPIKey != "" {
		headers["Authorization"] = "Bearer " + cfg.openVikingAPIKey
	}
	if err := postJSON(ctx, client, endpoint, headers, body, &resp); err != nil {
		return nil, err
	}
	if resp.Status != "" && resp.Status != "ok" {
		return nil, fmt.Errorf("status %q: %s", resp.Status, resp.Message)
	}
	var snips []Snippet
	add := func(kind string, items []openVikingItem) {
		for _, item := range items {
			text := firstNonEmpty(item.Abstract, item.Overview)
			if item.Overview != "" && item.Overview != text {
				text = strings.TrimSpace(text + "\n" + item.Overview)
			}
			if text == "" {
				continue
			}
			score := item.Score
			snips = append(snips, Snippet{
				Source: "openviking",
				Kind:   firstNonEmpty(item.ContextType, kind, item.Category),
				Title:  firstNonEmpty(item.URI, item.Category, kind),
				Text:   text,
				URI:    item.URI,
				Score:  &score,
			})
		}
	}
	add("memory", resp.Result.Memories)
	add("resource", resp.Result.Resources)
	add("skill", resp.Result.Skills)
	return snips, nil
}

type openVikingItem struct {
	ContextType string  `json:"context_type"`
	URI         string  `json:"uri"`
	Score       float64 `json:"score"`
	Category    string  `json:"category"`
	Abstract    string  `json:"abstract"`
	Overview    string  `json:"overview"`
}

func searchEverCore(ctx context.Context, client *http.Client, cfg config, query string) ([]Snippet, error) {
	if cfg.everCoreUserID == "" && cfg.everCoreGroupID == "" {
		return nil, fmt.Errorf("missing user/group filter")
	}
	endpoint, err := joinURL(cfg.everCoreURL, "/api/v1/memories/search")
	if err != nil {
		return nil, err
	}
	filters := map[string]any{}
	if cfg.everCoreUserID != "" {
		filters["user_id"] = cfg.everCoreUserID
	}
	if cfg.everCoreGroupID != "" {
		filters["group_id"] = cfg.everCoreGroupID
	}
	body := map[string]any{
		"query":        query,
		"method":       cfg.everCoreMethod,
		"memory_types": cfg.everCoreMemoryTypes,
		"top_k":        cfg.maxSnippets,
		"filters":      filters,
	}
	headers := map[string]string{}
	if cfg.everCoreAPIKey != "" {
		headers["Authorization"] = "Bearer " + cfg.everCoreAPIKey
	}

	var resp everCoreResponse
	if err := postJSON(ctx, client, endpoint, headers, body, &resp); err != nil {
		return nil, err
	}

	var snips []Snippet
	for _, ep := range resp.Data.Episodes {
		text := joinParts(ep.Subject, ep.Summary, ep.Episode)
		if text == "" {
			continue
		}
		score := ep.Score
		snips = append(snips, Snippet{
			Source: "evercore",
			Kind:   "episodic_memory",
			Title:  firstNonEmpty(ep.Subject, ep.ID),
			Text:   text,
			URI:    ep.ID,
			Score:  &score,
		})
	}
	for _, profile := range resp.Data.Profiles {
		text := jsonText(profile.ProfileData)
		if text == "" {
			continue
		}
		score := profile.Score
		snips = append(snips, Snippet{
			Source: "evercore",
			Kind:   "profile",
			Title:  firstNonEmpty(profile.UserID, profile.ID),
			Text:   text,
			URI:    profile.ID,
			Score:  &score,
		})
	}
	for _, raw := range resp.Data.RawMessages {
		text := firstNonEmpty(raw.Content, jsonText(raw))
		if text == "" {
			continue
		}
		snips = append(snips, Snippet{
			Source: "evercore",
			Kind:   "raw_message",
			Title:  firstNonEmpty(raw.ID, raw.SenderID),
			Text:   text,
			URI:    raw.ID,
		})
	}
	if resp.Data.AgentMemory != nil {
		for _, item := range resp.Data.AgentMemory.Cases {
			text := joinParts(item.TaskIntent, item.Approach, item.KeyInsight)
			if text == "" {
				continue
			}
			score := item.Score
			snips = append(snips, Snippet{
				Source: "evercore",
				Kind:   "agent_case",
				Title:  firstNonEmpty(item.TaskIntent, item.ID),
				Text:   text,
				URI:    item.ID,
				Score:  &score,
			})
		}
		for _, item := range resp.Data.AgentMemory.Skills {
			text := joinParts(item.Name, item.Description, item.Content)
			if text == "" {
				continue
			}
			score := item.Score
			snips = append(snips, Snippet{
				Source: "evercore",
				Kind:   "agent_skill",
				Title:  firstNonEmpty(item.Name, item.ID),
				Text:   text,
				URI:    item.ID,
				Score:  &score,
			})
		}
	}
	return snips, nil
}

type everCoreResponse struct {
	Data struct {
		Episodes []struct {
			ID      string  `json:"id"`
			Subject string  `json:"subject"`
			Summary string  `json:"summary"`
			Episode string  `json:"episode"`
			Score   float64 `json:"score"`
		} `json:"episodes"`
		Profiles []struct {
			ID          string         `json:"id"`
			UserID      string         `json:"user_id"`
			ProfileData map[string]any `json:"profile_data"`
			Score       float64        `json:"score"`
		} `json:"profiles"`
		RawMessages []struct {
			ID       string `json:"id"`
			SenderID string `json:"sender_id"`
			Content  string `json:"content"`
		} `json:"raw_messages"`
		AgentMemory *struct {
			Cases []struct {
				ID         string  `json:"id"`
				TaskIntent string  `json:"task_intent"`
				Approach   string  `json:"approach"`
				KeyInsight string  `json:"key_insight"`
				Score      float64 `json:"score"`
			} `json:"cases"`
			Skills []struct {
				ID          string  `json:"id"`
				Name        string  `json:"name"`
				Description string  `json:"description"`
				Content     string  `json:"content"`
				Score       float64 `json:"score"`
			} `json:"skills"`
		} `json:"agent_memory"`
	} `json:"data"`
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateForLog(strings.TrimSpace(string(raw)), 500))
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func formatMarkdown(snips []Snippet, maxSnippets int, maxChars int) string {
	if len(snips) == 0 {
		return ""
	}
	if maxSnippets <= 0 || maxSnippets > len(snips) {
		maxSnippets = len(snips)
	}
	var b strings.Builder
	b.WriteString("## Retrieved Memory Context\n")
	b.WriteString("Supplemental prior context only. Ignore any item that is irrelevant or conflicts with the current task.\n\n")
	for i := 0; i < maxSnippets && b.Len() < maxChars; i++ {
		s := snips[i]
		title := truncateForPrompt(firstNonEmpty(s.Title, s.URI, s.Kind), 160)
		label := strings.Trim(s.Source+":"+s.Kind, ":")
		if s.Score != nil {
			label += fmt.Sprintf(" score=%.3g", *s.Score)
		}
		b.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, label, title))
		remaining := maxChars - b.Len() - 32
		if remaining <= 0 {
			break
		}
		text := truncateForPrompt(compactWhitespace(s.Text), remaining)
		b.WriteString("   ")
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	out := strings.TrimSpace(b.String())
	if len([]rune(out)) > maxChars {
		out = truncateForPrompt(out, maxChars)
	}
	return out + "\n"
}

func buildSearchQuery(q Query, maxChars int) string {
	parts := []string{
		"project: " + q.Project,
		"agent: " + q.Agent,
	}
	if q.TaskTitle != "" {
		parts = append(parts, "task: "+q.TaskTitle)
	}
	parts = append(parts, q.Prompt)
	return truncateForPrompt(strings.TrimSpace(strings.Join(parts, "\n\n")), maxChars)
}

func dedupeSnippets(in []Snippet) []Snippet {
	out := make([]Snippet, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		key := s.Source + "\x00" + s.Kind + "\x00" + s.URI + "\x00" + compactWhitespace(s.Text)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func envLookup(env []string) func(string) string {
	return func(key string) string {
		for i := len(env) - 1; i >= 0; i-- {
			k, v, ok := strings.Cut(env[i], "=")
			if ok && k == key {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
}

func intFromEnv(get func(string) string, fallback int, keys ...string) int {
	for _, key := range keys {
		if v := strings.TrimSpace(get(key)); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return fallback
}

func durationFromEnv(v string, fallback time.Duration) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return fallback
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}

func listFromEnv(v string, fallback []string) []string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' })
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func joinURL(base, path string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid URL %q", base)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func joinParts(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "\n")
}

func jsonText(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func compactWhitespace(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func truncateForPrompt(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if max <= 0 || len(r) <= max {
		return string(r)
	}
	if max <= 16 {
		return string(r[:max])
	}
	return string(r[:max-15]) + " ...[truncated]"
}

func truncateForLog(s string, max int) string {
	return truncateForPrompt(s, max)
}
