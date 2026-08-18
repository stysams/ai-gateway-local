package endpoint

import "testing"

func TestJoinPresetAddsV1(t *testing.T) {
	cases := []struct {
		base, adapter, want string
	}{
		{"https://agentrouter.org", Responses, "https://agentrouter.org/v1/responses"},
		{"https://agentrouter.org", Messages, "https://agentrouter.org/v1/messages"},
		{"https://agentrouter.org", Chat, "https://agentrouter.org/v1/chat/completions"},
		{"https://openrouter.ai/api/v1", Chat, "https://openrouter.ai/api/v1/chat/completions"},
		{"https://api.openai.com/v1/", Responses, "https://api.openai.com/v1/responses"},
		{"https://api.anthropic.com", Messages, "https://api.anthropic.com/v1/messages"},
		{"https://api.anthropic.com/v1", Messages, "https://api.anthropic.com/v1/messages"},
		{"https://api.deepseek.com", Chat, "https://api.deepseek.com/v1/chat/completions"},
		{"https://api.x.ai", Responses, "https://api.x.ai/v1/responses"},
	}
	for _, tc := range cases {
		if got := Join(tc.base, tc.adapter, ""); got != tc.want {
			t.Errorf("Join(%q, %q) = %q, want %q", tc.base, tc.adapter, got, tc.want)
		}
	}
}

func TestJoinCustomUsesExactPath(t *testing.T) {
	got := Join("https://api.2dou.net", Responses, "/responses")
	if got != "https://api.2dou.net/responses" {
		t.Fatalf("custom join = %q", got)
	}
	got = Join("https://example.com/v1", Chat, "/chat/completions")
	if got != "https://example.com/v1/chat/completions" {
		t.Fatalf("custom join with /v1 base = %q", got)
	}
}

func TestInferWireAndValidateCustom(t *testing.T) {
	if wire, ok := InferWire("/v1/chat/completions"); !ok || wire != Chat {
		t.Fatalf("infer chat = %q %v", wire, ok)
	}
	if wire, ok := InferWire("/responses"); !ok || wire != Responses {
		t.Fatalf("infer responses = %q %v", wire, ok)
	}
	if wire, ok := InferWire("/v1/messages"); !ok || wire != Messages {
		t.Fatalf("infer messages = %q %v", wire, ok)
	}
	if _, ok := InferWire("/v1"); ok {
		t.Fatal("bare /v1 must not infer a wire protocol")
	}
	if err := ValidateCustom("/responses"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCustom("responses"); err == nil {
		t.Fatal("relative path must be rejected")
	}
	if err := ValidateCustom("/v1/foo"); err == nil {
		t.Fatal("unknown suffix must be rejected")
	}
	if err := ValidateCustom("/responses?x=1"); err == nil {
		t.Fatal("query string must be rejected")
	}
}
