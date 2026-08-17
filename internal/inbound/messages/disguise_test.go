package messages

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplyClaudeDisguiseAddsThinkingAndSystemCache(t *testing.T) {
	body := []byte(`{
		"model":"any/claude-fable-5",
		"max_tokens":8192,
		"output_config":{"effort":"medium"},
		"system":[{"type":"text","text":"You are Ally."}],
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],
		"tools":[{"name":"read","input_schema":{"type":"object"}}]
	}`)
	out, applied, err := ApplyClaudeDisguise(body, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(applied, ",") != "thinking,system_cache_control" {
		t.Fatalf("applied = %v", applied)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if string(doc["thinking"]) != `{"type":"adaptive"}` {
		t.Fatalf("thinking = %s", doc["thinking"])
	}
	if string(doc["output_config"]) != `{"effort":"medium"}` {
		t.Fatalf("output_config rewritten: %s", doc["output_config"])
	}
	var system []map[string]json.RawMessage
	if err := json.Unmarshal(doc["system"], &system); err != nil {
		t.Fatal(err)
	}
	if len(system) != 1 || string(system[0]["text"]) != `"You are Ally."` {
		t.Fatalf("system text changed: %s", doc["system"])
	}
	if string(system[0]["cache_control"]) != `{"type":"ephemeral"}` {
		t.Fatalf("system cache = %s", system[0]["cache_control"])
	}
	if !strings.Contains(string(doc["tools"]), `"read"`) {
		t.Fatalf("tools lost: %s", doc["tools"])
	}
}

func TestApplyClaudeDisguiseKeepsExistingThinkingAndCache(t *testing.T) {
	body := []byte(`{
		"thinking":{"type":"enabled","budget_tokens":2048},
		"system":[{"type":"text","text":"Keep","cache_control":{"type":"ephemeral"}}],
		"messages":[]
	}`)
	out, applied, err := ApplyClaudeDisguise(body, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Fatalf("applied = %v, want none", applied)
	}
	if string(out) != string(body) && !jsonEqual(t, out, body) {
		t.Fatalf("body changed: %s", out)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if string(doc["thinking"]) != `{"type":"enabled","budget_tokens":2048}` {
		t.Fatalf("existing thinking overwritten: %s", doc["thinking"])
	}
}

func TestApplyClaudeDisguiseSkipsThinkingWhenReasoningDisabled(t *testing.T) {
	body := []byte(`{"system":"Be brief.","messages":[{"role":"system","content":"Deferred"}]}`)
	out, applied, err := ApplyClaudeDisguise(body, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(applied, ",") != "system_cache_control" {
		t.Fatalf("applied = %v", applied)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["thinking"]; ok {
		t.Fatalf("thinking added while reasoning disabled: %s", doc["thinking"])
	}
	var system []map[string]any
	if err := json.Unmarshal(doc["system"], &system); err != nil {
		t.Fatal(err)
	}
	if system[0]["text"] != "Be brief." {
		t.Fatalf("string system text = %#v", system[0]["text"])
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(doc["messages"], &messages); err != nil {
		t.Fatal(err)
	}
	var content []map[string]any
	if err := json.Unmarshal(messages[0]["content"], &content); err != nil {
		t.Fatal(err)
	}
	if content[0]["text"] != "Deferred" {
		t.Fatalf("system message text = %#v", content[0]["text"])
	}
	cache, _ := content[0]["cache_control"].(map[string]any)
	if cache["type"] != "ephemeral" {
		t.Fatalf("system message cache = %#v", content[0]["cache_control"])
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var left, right any
	if err := json.Unmarshal(a, &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &right); err != nil {
		t.Fatal(err)
	}
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}
