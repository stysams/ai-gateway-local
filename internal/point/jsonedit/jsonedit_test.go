package jsonedit

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const settings = `{
  "permissions": {
    "allow": ["Read", "Bash(git:*)"]
  },
  "env": {
    "KEEP_ME": "yes",
    "ANTHROPIC_MODEL": "old-model"
  },
  "enabledMcpjsonServers": ["context7"],
  "statusLine": { "type": "command", "command": "ccstatus" }
}
`

func TestSetObjectStringsKeepsEverythingElseVerbatim(t *testing.T) {
	out, err := SetObjectStrings([]byte(settings), "env", []KV{
		{Key: "ANTHROPIC_MODEL", Value: "gateway-default"},
		{Key: "ANTHROPIC_BASE_URL", Value: "http://127.0.0.1:12600/c/claude"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, keep := range []string{
		`"allow": ["Read", "Bash(git:*)"]`,
		`"enabledMcpjsonServers": ["context7"],`,
		`"statusLine": { "type": "command", "command": "ccstatus" }`,
		`"KEEP_ME": "yes",`,
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("unrelated member changed, %q missing:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "old-model") {
		t.Errorf("target value not replaced:\n%s", got)
	}
	want := "    \"ANTHROPIC_MODEL\": \"gateway-default\",\n    \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:12600/c/claude\"\n  },"
	if !strings.Contains(got, want) {
		t.Errorf("appended member not indented like its siblings:\n%s", got)
	}
	assertValid(t, out)
}

func TestSetObjectStringsCreatesMissingMember(t *testing.T) {
	out, err := SetObjectStrings([]byte("{\n  \"permissions\": {\n    \"allow\": []\n  }\n}\n"), "env", []KV{
		{Key: "ANTHROPIC_API_KEY", Value: "sk-ai-gateway-local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"permissions\": {\n    \"allow\": []\n  },\n  \"env\": {\n    \"ANTHROPIC_API_KEY\": \"sk-ai-gateway-local\"\n  }\n}\n"
	if string(out) != want {
		t.Errorf("want %q\ngot  %q", want, out)
	}
}

func TestSetObjectStringsFillsEmptyObjects(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty document", "", "{\n  \"env\": {\n    \"K\": \"v\"\n  }\n}\n"},
		{"empty root", "{}", "{\n  \"env\": {\n    \"K\": \"v\"\n  }\n}"},
		{"empty member", "{\n  \"env\": {}\n}\n", "{\n  \"env\": {\n    \"K\": \"v\"\n  }\n}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := SetObjectStrings([]byte(tc.input), "env", []KV{{Key: "K", Value: "v"}})
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != tc.want {
				t.Errorf("want %q\ngot  %q", tc.want, out)
			}
			assertValid(t, out)
		})
	}
}

func TestSetObjectStringsRejectsNonObjectMember(t *testing.T) {
	_, err := SetObjectStrings([]byte(`{"env": "nope"}`), "env", []KV{{Key: "K", Value: "v"}})
	if !errors.Is(err, ErrNotObject) {
		t.Fatalf("error = %v, want ErrNotObject", err)
	}
}

func TestSetObjectStringsRejectsBrokenInput(t *testing.T) {
	for _, input := range []string{`{"env": {}} {"env": {}}`, `{"env": `, `[1,2]`, `{"env": {} `} {
		if _, err := SetObjectStrings([]byte(input), "env", []KV{{Key: "K", Value: "v"}}); err == nil {
			t.Errorf("input %q was accepted", input)
		}
	}
}

// Values are written without HTML escaping so a base URL keeps its bytes, and
// numbers or nested structures elsewhere in the document are not reformatted.
func TestValuesAndNeighboursKeepTheirBytes(t *testing.T) {
	input := "{\n  \"cleanupPeriodDays\": 20,\n  \"env\": {\n    \"A\": \"1\"\n  },\n  \"hooks\": {\"Stop\": [{\"matcher\": \"*\"}]}\n}\n"
	out, err := SetObjectStrings([]byte(input), "env", []KV{{Key: "B", Value: "x?a=1&b=2<3"}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `"cleanupPeriodDays": 20,`) || !strings.Contains(got, `"hooks": {"Stop": [{"matcher": "*"}]}`) {
		t.Errorf("neighbouring members were reformatted:\n%s", got)
	}
	if !strings.Contains(got, `"B": "x?a=1&b=2<3"`) {
		t.Errorf("value was escaped:\n%s", got)
	}
	assertValid(t, out)
}

// Windows settings can use CRLF. Inserted members must follow the file's own
// line ending instead of leaving it mixed.
func TestCRLFDocumentKeepsItsLineEndings(t *testing.T) {
	out, err := SetObjectStrings([]byte("{\r\n  \"env\": {\r\n    \"A\": \"1\"\r\n  }\r\n}\r\n"), "env", []KV{
		{Key: "A", Value: "2"},
		{Key: "B", Value: "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\r\n  \"env\": {\r\n    \"A\": \"2\",\r\n    \"B\": \"3\"\r\n  }\r\n}\r\n"
	if string(out) != want {
		t.Errorf("want %q\ngot  %q", want, out)
	}
	assertValid(t, out)
}

func assertValid(t *testing.T, data []byte) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, data)
	}
}
