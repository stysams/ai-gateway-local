package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"ai-gateway/internal/point/clientcatalog"
)

// §12.4 forbids overwriting unrelated `env` variables. §12.1 and the
// 2026-08-21 record in §20 extend that to the rest of settings.json: hooks,
// permissions, status line and MCP switches keep their own bytes and order.
func TestTransformMergesEnvWithoutReflowingSettings(t *testing.T) {
	base := "http://127.0.0.1:12600"
	original := `{
  "permissions": {
    "allow": ["Read", "Bash(git status:*)"]
  },
  "enabledMcpjsonServers": ["example-docs"],
  "hooks": {
    "Stop": [{ "matcher": "*" }]
  },
  "cleanupPeriodDays": 30,
  "env": {
    "KEEP_ME": "yes",
    "ANTHROPIC_MODEL": "example-old-model"
  }
}
`
	settings := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel}
	out, err := Transform([]byte(original), base, settings)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, keep := range []string{
		"\"allow\": [\"Read\", \"Bash(git status:*)\"]",
		"\"enabledMcpjsonServers\": [\"example-docs\"],",
		"\"Stop\": [{ \"matcher\": \"*\" }]",
		"\"cleanupPeriodDays\": 30,",
		"\"KEEP_ME\": \"yes\",",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("unrelated setting %q was disturbed:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "example-old-model") {
		t.Errorf("gateway model slot was not rewritten:\n%s", got)
	}
	ok, err := Check(out, base, settings)
	if err != nil || !ok {
		t.Fatalf("Check after transform: ok=%v err=%v\n%s", ok, err, got)
	}
}

func TestTransformIsIdempotent(t *testing.T) {
	base := "http://127.0.0.1:12600"
	settings := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel}
	first, err := Transform([]byte("{\n  \"cleanupPeriodDays\": 30\n}\n"), base, settings)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Transform(first, base, settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("repeated transform changed the file:\nfirst  %q\nsecond %q", first, second)
	}
}

func TestTransformSeparatesStartupSubagentAndTitleModels(t *testing.T) {
	settings := clientcatalog.Settings{
		PreferredModel: clientcatalog.ReservedModel,
		SubagentModel:  "openrouter/claude-opus-5",
		TitleModel:     "ollama/qwen3",
	}
	out, err := Transform([]byte(`{}`), "http://127.0.0.1:12600", settings)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(out, &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL"} {
		if got := document.Env[key]; got != settings.SubagentModel {
			t.Errorf("%s = %q, want %q", key, got, settings.SubagentModel)
		}
	}
	for _, key := range []string{"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
		if got := document.Env[key]; got != settings.TitleModel {
			t.Errorf("%s = %q, want %q", key, got, settings.TitleModel)
		}
	}
	if got := document.Env["ANTHROPIC_MODEL"]; got != clientcatalog.ReservedModel {
		t.Errorf("ANTHROPIC_MODEL = %q, want %q", got, clientcatalog.ReservedModel)
	}
}

func TestTransformRejectsNonObjectEnv(t *testing.T) {
	_, err := Transform([]byte(`{"env": "nope"}`), "http://127.0.0.1:12600", clientcatalog.Settings{})
	if err == nil || !strings.Contains(err.Error(), `field "env" must be an object`) {
		t.Fatalf("error = %v", err)
	}
}
