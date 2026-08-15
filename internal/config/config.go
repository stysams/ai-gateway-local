// Package config owns the single source of truth for ai-gateway: the
// config.yaml file. It provides the YAML model, defaults, full validation,
// loading and same-directory atomic writes. Unknown top-level YAML fields are
// retained on read and preserved on write-back.
package config

import (
	"gopkg.in/yaml.v3"
)

// Defaults for the config contract (docs/v1-scheme.md §5).
const (
	DefaultPort     = 12600
	DefaultLogDir   = "logs"
	DefaultLanguage = "zh-CN"

	// ConfigFileName is the fixed config file name inside the data root.
	ConfigFileName = "config.yaml"
)

// Config is the complete persisted business configuration. Extra captures
// unknown top-level YAML fields (yaml.v3 inline map) so they survive a
// read-modify-write cycle.
type Config struct {
	Version   int                  `yaml:"version"`
	Listen    Listen               `yaml:"listen,omitempty"`
	Logging   Logging              `yaml:"logging,omitempty"`
	UI        UI                   `yaml:"ui,omitempty"`
	Autostart Autostart            `yaml:"autostart,omitempty"`
	Providers map[string]Provider  `yaml:"providers,omitempty"`
	Routes    Routes               `yaml:"routes,omitempty"`
	Extra     map[string]yaml.Node `yaml:",inline"`
}

// Listen configures the loopback listener. The hostname is fixed to
// 127.0.0.1 and never stored in config.
type Listen struct {
	// Port uses a pointer so an explicitly invalid value (e.g. 0 or 80) is
	// rejected by validation instead of being silently replaced by the
	// default; a missing field falls back to DefaultPort.
	Port *int `yaml:"port,omitempty"`
}

// PortValue returns the effective port, applying DefaultPort when absent.
func (l Listen) PortValue() int {
	if l.Port != nil {
		return *l.Port
	}
	return DefaultPort
}

// Logging controls per-request JSONL body logging.
type Logging struct {
	// Enabled uses a pointer to distinguish "absent" (default true) from an
	// explicit false.
	Enabled *bool `yaml:"enabled,omitempty"`
	// Dir is relative to the data root; empty means DefaultLogDir.
	Dir string `yaml:"dir,omitempty"`
}

// EnabledValue returns the effective enabled flag (default true).
func (l Logging) EnabledValue() bool {
	if l.Enabled != nil {
		return *l.Enabled
	}
	return true
}

// UI carries desktop-only preferences. The headless gateway parses and
// preserves them.
type UI struct {
	Language              string `yaml:"language,omitempty"`
	LoggingNoticeAccepted bool   `yaml:"logging_notice_accepted,omitempty"`
}

// Autostart is a no-op in task package A; the flag is part of the frozen
// config contract and is surfaced by the management API.
type Autostart struct {
	Enabled bool `yaml:"enabled,omitempty"`
}

// Provider is a single upstream provider definition.
type Provider struct {
	Name         string       `yaml:"name"`
	Adapter      string       `yaml:"adapter"`
	BaseURL      string       `yaml:"base_url"`
	DefaultModel string       `yaml:"default_model"`
	SecretRef    string       `yaml:"secret_ref,omitempty"`
	Capabilities Capabilities `yaml:"capabilities,omitempty"`
}

// Capabilities describes provider feature flags.
type Capabilities struct {
	ImageInput bool `yaml:"image_input,omitempty"`
	Reasoning  bool `yaml:"reasoning,omitempty"`
}

// Routes is the fixed set of four first-class client routes.
type Routes struct {
	Codex   Route `yaml:"codex"`
	Claude  Route `yaml:"claude"`
	Grok    Route `yaml:"grok"`
	Generic Route `yaml:"generic"`
}

// Route points one client at one provider/model pair.
type Route struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// route returns the route for one of the four fixed client ids.
func (r Routes) route(name string) Route {
	switch name {
	case "codex":
		return r.Codex
	case "claude":
		return r.Claude
	case "grok":
		return r.Grok
	case "generic":
		return r.Generic
	}
	return Route{}
}

// Defaults returns the initial configuration generated when no config file
// exists. It matches the shape of the full example in docs/v1-scheme.md §5.1
// but deliberately omits secret_ref: secrets belong to the system key store
// (task package B), and a default config must not require one to start.
func Defaults() *Config {
	return &Config{
		Version:   1,
		Listen:    Listen{Port: IntPtr(DefaultPort)},
		Logging:   Logging{Enabled: BoolPtr(true), Dir: DefaultLogDir},
		UI:        UI{Language: DefaultLanguage},
		Autostart: Autostart{Enabled: false},
		Providers: map[string]Provider{
			"openrouter": {
				Name:         "OpenRouter",
				Adapter:      "openai-chat",
				BaseURL:      "https://openrouter.ai/api/v1",
				DefaultModel: "anthropic/claude-sonnet-4",
				Capabilities: Capabilities{ImageInput: true, Reasoning: true},
			},
			"ollama": {
				Name:         "Ollama",
				Adapter:      "openai-chat",
				BaseURL:      "http://127.0.0.1:11434/v1",
				DefaultModel: "qwen3",
			},
		},
		Routes: Routes{
			Codex:   Route{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"},
			Claude:  Route{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"},
			Grok:    Route{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"},
			Generic: Route{Provider: "ollama", Model: "qwen3"},
		},
	}
}

// normalize fills missing optional fields with their defaults. It must run
// after decoding a user-provided file and never causes a rewrite by itself.
func (c *Config) normalize() {
	if c.Logging.Dir == "" {
		c.Logging.Dir = DefaultLogDir
	}
	if c.UI.Language == "" {
		c.UI.Language = DefaultLanguage
	}
}

// clone returns a deep copy safe to hand out as a snapshot.
func (c *Config) clone() *Config {
	out := *c
	if c.Listen.Port != nil {
		p := *c.Listen.Port
		out.Listen.Port = &p
	}
	if c.Logging.Enabled != nil {
		b := *c.Logging.Enabled
		out.Logging.Enabled = &b
	}
	if c.Providers != nil {
		out.Providers = make(map[string]Provider, len(c.Providers))
		for k, v := range c.Providers {
			out.Providers[k] = v
		}
	}
	if c.Extra != nil {
		out.Extra = make(map[string]yaml.Node, len(c.Extra))
		for k, v := range c.Extra {
			out.Extra[k] = v
		}
	}
	return &out
}

// IntPtr returns a pointer to v; used to distinguish absent optional fields.
func IntPtr(v int) *int { return &v }

// BoolPtr returns a pointer to v; used to distinguish absent optional fields.
func BoolPtr(v bool) *bool { return &v }
