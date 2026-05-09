package telemetry

import (
	"testing"
)

func TestParseStreamJSONUsage_Claude(t *testing.T) {
	data := []byte(`{"type":"assistant","message":{"role":"assistant"}}
{"type":"result","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":20},"total_cost_usd":0.0123}
`)
	u := ParseStreamJSONUsage(data)
	if !u.SawResult {
		t.Fatal("expected SawResult")
	}
	if u.InputTokens != 100 || u.OutputTokens != 50 || u.CacheReadTokens != 20 {
		t.Fatalf("tokens: %+v", u)
	}
	if u.TotalCostUSD != 0.0123 {
		t.Fatalf("cost: %v", u.TotalCostUSD)
	}
}

func TestParseStreamJSONUsage_Cursor(t *testing.T) {
	data := []byte(`{"type":"system","subtype":"init","session_id":"abc","model":"Opus 4.6"}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]},"session_id":"abc"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]},"session_id":"abc"}
{"type":"result","subtype":"success","duration_ms":7361,"is_error":false,"result":"ok","session_id":"abc","usage":{"inputTokens":300,"outputTokens":40,"cacheReadTokens":16000,"cacheWriteTokens":2500}}
`)
	u := ParseStreamJSONUsage(data)
	if !u.SawResult {
		t.Fatal("expected SawResult")
	}
	if u.InputTokens != 300 {
		t.Fatalf("InputTokens: got %d, want 300", u.InputTokens)
	}
	if u.OutputTokens != 40 {
		t.Fatalf("OutputTokens: got %d, want 40", u.OutputTokens)
	}
	if u.CacheReadTokens != 16000 {
		t.Fatalf("CacheReadTokens: got %d, want 16000", u.CacheReadTokens)
	}
}

func TestParseStreamJSONUsage_CodexTokenCount(t *testing.T) {
	data := []byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":602901,"cached_input_tokens":528128,"output_tokens":3752,"reasoning_output_tokens":2048,"total_tokens":606653}}}}
`)
	u := ParseStreamJSONUsage(data)
	if !u.SawResult {
		t.Fatal("expected SawResult")
	}
	if u.InputTokens != 602901 {
		t.Fatalf("InputTokens: got %d, want 602901", u.InputTokens)
	}
	if u.OutputTokens != 3752 {
		t.Fatalf("OutputTokens: got %d, want 3752", u.OutputTokens)
	}
	if u.CacheReadTokens != 528128 {
		t.Fatalf("CacheReadTokens: got %d, want 528128", u.CacheReadTokens)
	}
}

func TestParseCodexTextUsage(t *testing.T) {
	data := []byte("session id: 019e01cb-2146-79a3-ab34-54e799203721\n\ntokens used\n78,525\n")
	u := ParseCodexTextUsage(data)
	if !u.SawResult {
		t.Fatal("expected SawResult")
	}
	if !u.TotalOnly {
		t.Fatal("expected TotalOnly")
	}
	if u.InputTokens != 78525 {
		t.Fatalf("InputTokens: got %d, want 78525", u.InputTokens)
	}
}
