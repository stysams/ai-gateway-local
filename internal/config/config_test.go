package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDefaults(t *testing.T) {
	c := Defaults()
	if c.Version != 1 {
		t.Errorf("Version = %d, want 1", c.Version)
	}
	if c.Listen.PortValue() != DefaultPort {
		t.Errorf("Port = %d, want %d", c.Listen.PortValue(), DefaultPort)
	}
	if c.Logging.EnabledValue() != true {
		t.Error("Logging.EnabledValue() = false, want true")
	}
	if c.Logging.Dir != DefaultLogDir {
		t.Errorf("Logging.Dir = %q, want %q", c.Logging.Dir, DefaultLogDir)
	}
	if c.UI.Language != DefaultLanguage {
		t.Errorf("UI.Language = %q, want %q", c.UI.Language, DefaultLanguage)
	}
	if c.Autostart.Enabled {
		t.Error("Autostart.Enabled = true, want false")
	}
	for _, id := range []string{"openrouter", "ollama"} {
		if _, ok := c.Providers[id]; !ok {
			t.Errorf("default provider %q missing", id)
		}
	}
	for _, name := range []string{"codex", "claude", "grok", "generic"} {
		r := c.Routes.route(name)
		if r.Provider == "" || r.Model == "" {
			t.Errorf("default route %q incomplete: %+v", name, r)
		}
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Defaults().Validate() = %v, want nil", err)
	}
}

func TestLoadMissingCreatesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	m := NewManager(path)
	cfg, err := m.LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if cfg.Version != 1 || cfg.Listen.PortValue() != DefaultPort {
		t.Errorf("generated config wrong: %+v", cfg)
	}
	data := readFile(t, path)
	if !strings.Contains(string(data), "openrouter") || !strings.Contains(string(data), "ollama") {
		t.Error("generated config file missing default providers")
	}
	// Generated file must be loadable and valid on its own.
	if _, err := Parse(data); err != nil {
		t.Errorf("generated file fails Parse: %v", err)
	}
}

func TestParseInvalidYAMLDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "version: [unclosed\nlisten:\n  port: 12600\n"
	writeConfig(t, path, original)

	m := NewManager(path)
	if _, err := m.Load(); err == nil {
		t.Fatal("Load of invalid YAML succeeded, want error")
	}
	if got := string(readFile(t, path)); got != original {
		t.Errorf("config file was modified after failed parse:\n%s", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestInvalidConfigDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `version: 1
listen:
  port: 99999
providers: {}
routes:
  codex: {provider: x, model: y}
  claude: {provider: x, model: y}
  grok: {provider: x, model: y}
  generic: {provider: x, model: y}
`
	writeConfig(t, path, original)

	m := NewManager(path)
	_, err := m.Load()
	if err == nil {
		t.Fatal("Load of invalid config succeeded, want validation error")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if ve.File != path {
		t.Errorf("ValidationError.File = %q, want %q", ve.File, path)
	}
	if !strings.Contains(ve.Error(), "listen.port") {
		t.Errorf("error missing locatable field: %v", ve)
	}
	if !strings.Contains(ve.Error(), "routes.codex.provider") {
		t.Errorf("error missing route field: %v", ve)
	}
	if got := string(readFile(t, path)); got != original {
		t.Errorf("config file was modified after validation failure:\n%s", got)
	}
}

func TestParseInvalidYAML(t *testing.T) {
	if _, err := Parse([]byte("version: [unclosed\n")); err == nil {
		t.Fatal("Parse of invalid YAML succeeded, want error")
	}
	if _, err := Parse([]byte("version: notanumber\nlisten:\n  port: 12600\n")); err == nil {
		t.Fatal("Parse of bad version type succeeded, want error")
	}
}

func TestValidateVersion(t *testing.T) {
	for _, v := range []int{0, 2, 3} {
		c := Defaults()
		c.Version = v
		if err := c.Validate(); err == nil {
			t.Errorf("Version %d accepted, want rejection", v)
		}
	}
}

func TestValidatePortRange(t *testing.T) {
	cases := []struct {
		port   *int
		wantOK bool
	}{
		{nil, true}, // absent -> default 12600
		{IntPtr(12600), true},
		{IntPtr(1024), true},
		{IntPtr(65535), true},
		{IntPtr(1023), false},
		{IntPtr(65536), false},
		{IntPtr(0), false},
		{IntPtr(80), false},
		{IntPtr(-1), false},
	}
	for _, tc := range cases {
		c := Defaults()
		c.Listen.Port = tc.port
		err := c.Validate()
		if (err == nil) != tc.wantOK {
			got := "accepted"
			if err != nil {
				got = err.Error()
			}
			t.Errorf("port %v: %s, want ok=%v", tc.port, got, tc.wantOK)
		}
	}
}

func TestValidateProviderID(t *testing.T) {
	cases := []struct {
		id     string
		wantOK bool
	}{
		{"openrouter", true},
		{"a", true},
		{"a1", true},
		{"a-b_c", true},
		{strings.Repeat("a", 32), true},
		{"OpenRouter", false},
		{"1abc", false},
		{"_abc", false},
		{"a.b", false},
		{strings.Repeat("a", 33), false},
	}
	for _, tc := range cases {
		c := Defaults()
		p := c.Providers["ollama"]
		c.Providers = map[string]Provider{tc.id: p}
		c.Routes = Routes{
			Codex:   Route{Provider: tc.id, Model: "m"},
			Claude:  Route{Provider: tc.id, Model: "m"},
			Grok:    Route{Provider: tc.id, Model: "m"},
			Generic: Route{Provider: tc.id, Model: "m"},
		}
		err := c.Validate()
		if (err == nil) != tc.wantOK {
			got := "accepted"
			if err != nil {
				got = err.Error()
			}
			t.Errorf("provider id %q: %s, want ok=%v", tc.id, got, tc.wantOK)
		}
	}
}

func TestValidateAdapter(t *testing.T) {
	for _, ok := range []struct {
		adapter string
		wantOK  bool
	}{
		{"openai-chat", true},
		{"openai-responses", true},
		{"anthropic", true},
		{"chat", false},
		{"", false},
		{"openai", false},
	} {
		c := Defaults()
		p := c.Providers["ollama"]
		p.Adapter = ok.adapter
		c.Providers["ollama"] = p
		err := c.Validate()
		if (err == nil) != ok.wantOK {
			t.Errorf("adapter %q accepted=%v, want %v (err=%v)", ok.adapter, err == nil, ok.wantOK, err)
		}
	}
}

func TestValidateBaseURL(t *testing.T) {
	cases := []struct {
		raw    string
		wantOK bool
	}{
		{"https://openrouter.ai/api/v1", true},
		{"http://127.0.0.1:11434/v1", true},
		{"https://api.openai.com/v1", true},
		{"", false},
		{"/relative/path", false},
		{"openrouter.ai", false},
		{"ftp://openrouter.ai", false},
		{"https://x.ai/v1?q=1", false},
		{"https://x.ai/v1#frag", false},
	}
	for _, tc := range cases {
		if got := validBaseURL(tc.raw); got != tc.wantOK {
			t.Errorf("validBaseURL(%q) = %v, want %v", tc.raw, got, tc.wantOK)
		}
	}
}

func TestValidateRoutes(t *testing.T) {
	t.Run("unknown provider reference", func(t *testing.T) {
		c := Defaults()
		c.Routes.Codex = Route{Provider: "missing", Model: "m"}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "references unknown provider") {
			t.Fatalf("want unknown-provider error, got %v", err)
		}
	})
	t.Run("empty model", func(t *testing.T) {
		c := Defaults()
		c.Routes.Claude = Route{Provider: "ollama", Model: "  "}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "routes.claude.model") {
			t.Fatalf("want model error, got %v", err)
		}
	})
	t.Run("missing route", func(t *testing.T) {
		c := Defaults()
		c.Routes.Grok = Route{}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "routes.grok") {
			t.Fatalf("want missing-route error, got %v", err)
		}
	})
}

func TestValidateProvider(t *testing.T) {
	p := Defaults().Providers["ollama"]
	if err := ValidateProvider("ollama", p); err != nil {
		t.Fatalf("valid provider rejected: %v", err)
	}

	cases := []struct {
		id   string
		p    Provider
		want string // substring of the expected error, "" means valid
	}{
		{"BadID", p, "providers.BadID"},
		{"", p, "providers."},
		{"ok", Provider{Name: "", Adapter: "openai-chat", BaseURL: "https://x.ai", DefaultModel: "m"}, "providers.ok.name"},
		{"ok", Provider{Name: "x", Adapter: "bogus", BaseURL: "https://x.ai", DefaultModel: "m"}, "providers.ok.adapter"},
		{"ok", Provider{Name: "x", Adapter: "openai-chat", BaseURL: "relative", DefaultModel: "m"}, "providers.ok.base_url"},
		{"ok", Provider{Name: "x", Adapter: "openai-chat", BaseURL: "https://x.ai", DefaultModel: ""}, "providers.ok.default_model"},
		{"ok", Provider{Name: "x", Adapter: "openai-chat", BaseURL: "https://x.ai", DefaultModel: "m", Models: []ProviderModel{{ID: "m"}, {ID: "m"}}}, "duplicates model"},
		{"ok", Provider{Name: "x", Adapter: "openai-chat", BaseURL: "https://x.ai", DefaultModel: "missing", Models: []ProviderModel{{ID: "m"}}}, "must reference an entry in models"},
	}
	for _, tc := range cases {
		err := ValidateProvider(tc.id, tc.p)
		if tc.want == "" {
			if err != nil {
				t.Errorf("ValidateProvider(%q) = %v, want nil", tc.id, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ValidateProvider(%q) = %v, want error containing %q", tc.id, err, tc.want)
		}
	}
}

func TestValidateSecretRef(t *testing.T) {
	cases := []struct {
		ref    string
		wantOK bool
	}{
		{"", true}, // absent is fine
		{"provider.openrouter", true},
		{"Provider-1.x_y", true},
		{"provider/with/slash", false},
		{"provider with space", false},
		{"provider\\backslash", false},
		{"provider\t", false},
	}
	for _, tc := range cases {
		c := Defaults()
		p := c.Providers["ollama"]
		p.SecretRef = tc.ref
		c.Providers["ollama"] = p
		err := c.Validate()
		if (err == nil) != tc.wantOK {
			t.Errorf("secret_ref %q: accepted=%v, want %v (err=%v)", tc.ref, err == nil, tc.wantOK, err)
		}
	}
}

func TestUnknownTopLevelFieldsPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	input := `version: 1
custom_section:
  nested: [1, 2, three]
another_field: hello
listen:
  port: 13000
providers:
  ollama: {name: Ollama, adapter: openai-chat, base_url: http://127.0.0.1:11434/v1, default_model: qwen3}
routes:
  codex: {provider: ollama, model: qwen3}
  claude: {provider: ollama, model: qwen3}
  grok: {provider: ollama, model: qwen3}
  generic: {provider: ollama, model: qwen3}
`
	writeConfig(t, path, input)

	m := NewManager(path)
	cfg, err := m.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Extra["custom_section"]; !ok {
		t.Fatal("custom_section not captured in Extra")
	}
	if _, ok := cfg.Extra["another_field"]; !ok {
		t.Fatal("another_field not captured in Extra")
	}

	if err := m.Write(cfg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data := readFile(t, path)
	if !strings.Contains(string(data), "custom_section") {
		t.Errorf("written file lost custom_section:\n%s", data)
	}

	cfg2, err := m.Load()
	if err != nil {
		t.Fatalf("reload after write: %v", err)
	}
	node, ok := cfg2.Extra["custom_section"]
	if !ok {
		t.Fatal("custom_section lost after read-modify-write")
	}
	var section map[string]any
	if err := node.Decode(&section); err != nil {
		t.Fatalf("decode custom_section: %v", err)
	}
	nested, ok := section["nested"].([]any)
	if !ok {
		t.Fatalf("custom_section.nested missing: %v", section)
	}
	if len(nested) != 3 || nested[2] != "three" {
		t.Errorf("custom_section semantics changed: %v", nested)
	}
}

func TestWriteValidatesBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	m := NewManager(path)

	c := Defaults()
	c.Listen.Port = IntPtr(80) // invalid
	if err := m.Write(c); err == nil {
		t.Fatal("Write accepted invalid config")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Write created a file for an invalid config")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestWriteRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	m := NewManager(path)

	if err := m.Write(Defaults()); err != nil {
		t.Fatalf("Write defaults: %v", err)
	}
	cfg, err := m.Load()
	if err != nil {
		t.Fatalf("Load after write: %v", err)
	}
	if cfg.Listen.PortValue() != DefaultPort {
		t.Errorf("roundtrip port = %d", cfg.Listen.PortValue())
	}

	modified := Defaults()
	modified.Listen.Port = IntPtr(13000)
	modified.Logging.Enabled = BoolPtr(false)
	if err := m.Write(modified); err != nil {
		t.Fatalf("Write modified: %v", err)
	}
	cfg2, err := m.Load()
	if err != nil {
		t.Fatalf("Load after second write: %v", err)
	}
	if cfg2.Listen.PortValue() != 13000 {
		t.Errorf("port after second write = %d, want 13000", cfg2.Listen.PortValue())
	}
	if cfg2.Logging.EnabledValue() {
		t.Error("logging should be disabled after second write")
	}
	snap := m.Snapshot()
	if snap == nil || snap.Listen.PortValue() != 13000 {
		t.Error("snapshot not updated after successful write")
	}
}

func TestSnapshotIsolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	m := NewManager(path)
	if err := m.Write(Defaults()); err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot()
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	// Mutating the returned snapshot must not affect the manager.
	snap.Listen.Port = IntPtr(9999)
	if m.Snapshot().Listen.PortValue() == 9999 {
		t.Error("snapshot mutation leaked into manager")
	}
}

func TestConcurrentWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	m := NewManager(path)
	if err := m.Write(Defaults()); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			c := Defaults()
			c.Listen.Port = IntPtr(port)
			if err := m.Write(c); err != nil {
				t.Errorf("concurrent Write(%d): %v", port, err)
			}
		}(12000 + i)
	}
	wg.Wait()

	snap := m.Snapshot()
	data := readFile(t, path)
	disk, err := Parse(data)
	if err != nil {
		t.Fatalf("final file does not parse: %v", err)
	}
	if snap.Listen.PortValue() != disk.Listen.PortValue() {
		t.Errorf("snapshot port %d != disk port %d",
			snap.Listen.PortValue(), disk.Listen.PortValue())
	}
}

func TestParseAndNormalize(t *testing.T) {
	cfg, err := Parse([]byte(`version: 1
providers:
  ollama: {name: Ollama, adapter: openai-chat, base_url: http://127.0.0.1:11434/v1, default_model: qwen3}
routes:
  codex: {provider: ollama, model: qwen3}
  claude: {provider: ollama, model: qwen3}
  grok: {provider: ollama, model: qwen3}
  generic: {provider: ollama, model: qwen3}
`))
	if err != nil {
		t.Fatalf("Parse minimal config: %v", err)
	}
	if cfg.Listen.PortValue() != DefaultPort {
		t.Errorf("normalized port = %d", cfg.Listen.PortValue())
	}
	if !cfg.Logging.EnabledValue() {
		t.Error("normalized logging should default to enabled")
	}
	if cfg.Logging.Dir != DefaultLogDir || cfg.UI.Language != DefaultLanguage {
		t.Error("normalized defaults missing")
	}
}

func TestMarshalOmitsDeclaredCollisions(t *testing.T) {
	c := Defaults()
	c.Extra = map[string]yaml.Node{
		"version": {Kind: yaml.ScalarNode, Value: "999"},
		"brand":   {Kind: yaml.ScalarNode, Value: "x"},
	}
	data, err := Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "999") {
		t.Errorf("Extra key colliding with declared field leaked:\n%s", data)
	}
	if !strings.Contains(string(data), "brand: x") {
		t.Errorf("non-colliding Extra key lost:\n%s", data)
	}
}
