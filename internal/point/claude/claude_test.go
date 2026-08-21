package claude

import (
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

func TestTransformRejectsNonObjectEnv(t *testing.T) {
	_, err := Transform([]byte(`{"env": "nope"}`), "http://127.0.0.1:12600", clientcatalog.Settings{})
	if err == nil || !strings.Contains(err.Error(), `field "env" must be an object`) {
		t.Fatalf("error = %v", err)
	}
}
