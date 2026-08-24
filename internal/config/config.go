// Package config owns the single source of truth for ai-gateway: the
// config.yaml file. It provides the YAML model, defaults, full validation,
// loading and same-directory atomic writes. Unknown top-level YAML fields are
// retained on read and preserved on write-back.
package config

import (
	"sort"
	"strings"

	"ai-gateway/internal/endpoint"

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
	Version     int                  `yaml:"version"`
	Listen      Listen               `yaml:"listen,omitempty"`
	Logging     Logging              `yaml:"logging,omitempty"`
	Limits      Limits               `yaml:"limits,omitempty"`
	UI          UI                   `yaml:"ui,omitempty"`
	Autostart   Autostart            `yaml:"autostart,omitempty"`
	Providers   map[string]Provider  `yaml:"providers,omitempty"`
	Routes      Routes               `yaml:"routes,omitempty"`
	Clients     Clients              `yaml:"clients,omitempty"`
	Extra       map[string]yaml.Node `yaml:",inline"`
	modelOwners map[string][]string
}

// Limits bounds active data-plane requests. Zero disables a limit. A request
// is rejected immediately when any configured limit is already occupied; it
// is never queued behind an active request.
type Limits struct {
	Global              int `yaml:"global,omitempty"`
	PerClient           int `yaml:"per_client,omitempty"`
	PerProvider         int `yaml:"per_provider,omitempty"`
	StreamIdleSeconds   int `yaml:"stream_idle_seconds,omitempty"`
	RequestBodyBytes    int `yaml:"request_body_bytes,omitempty"`
	RequestHeaderBytes  int `yaml:"request_header_bytes,omitempty"`
	ClientRatePerMinute int `yaml:"client_rate_per_minute,omitempty"`
}

const (
	MaxConcurrencyLimit      = 1024
	DefaultStreamIdleSeconds = 300
	MaxStreamIdleSeconds     = 86400
	DefaultRequestBodyBytes  = 128 << 20
	MaxRequestHeaderBytes    = 1 << 20
	MaxClientRatePerMinute   = 100000
)

func (l Limits) RequestBodyBytesValue() int {
	if l.RequestBodyBytes <= 0 {
		return DefaultRequestBodyBytes
	}
	return l.RequestBodyBytes
}

// Clients holds per-client gateway preferences that are not routes.
// Desktop writes these through dedicated management endpoints, never by
// editing the client files directly.
type Clients struct {
	Codex  CodexClient  `yaml:"codex,omitempty"`
	Claude ClaudeClient `yaml:"claude,omitempty"`
}

// CodexClient is the Codex-only preference block.
type CodexClient struct {
	// RemoteCompaction, when true, makes a pointed Codex config advertise
	// the provider display name "OpenAI" so Codex sends
	// POST /responses/compact to the gateway (docs/v1-scheme.md §12.3).
	RemoteCompaction *bool  `yaml:"remote_compaction,omitempty"`
	SubagentModel    string `yaml:"subagent_model,omitempty"`
	TitleModel       string `yaml:"title_model,omitempty"`
}

func (c CodexClient) RemoteCompactionValue() bool {
	return c.RemoteCompaction != nil && *c.RemoteCompaction
}

// ClaudeClient controls the Claude Code model aliases that are written while
// the client is pointed at the gateway.
type ClaudeClient struct {
	SubagentModel string `yaml:"subagent_model,omitempty"`
	TitleModel    string `yaml:"title_model,omitempty"`
}

// Listen configures the shared HTTP listener. The management API remains
// loopback-only when Host is set to 0.0.0.0 for LAN data-plane access.
type Listen struct {
	// Host defaults to loopback. Set to 0.0.0.0 when other clients on the
	// local network must reach the gateway.
	Host string `yaml:"host,omitempty"`
	// Port uses a pointer so an explicitly invalid value (e.g. 0 or 80) is
	// rejected by validation instead of being silently replaced by the
	// default; a missing field falls back to DefaultPort.
	Port *int `yaml:"port,omitempty"`
}

func (l Listen) HostValue() string {
	if l.Host == "" {
		return "127.0.0.1"
	}
	return l.Host
}

// PortValue returns the effective port, applying DefaultPort when absent.
func (l Listen) PortValue() int {
	if l.Port != nil {
		return *l.Port
	}
	return DefaultPort
}

// Logging controls per-request JSONL request logging and optional body persistence.
type Logging struct {
	// Enabled uses a pointer to distinguish "absent" (default true) from an
	// explicit false.
	Enabled *bool `yaml:"enabled,omitempty"`
	// Body uses a pointer to distinguish "absent" (default true) from an
	// explicit false. It only takes effect when Enabled is true.
	Body *bool `yaml:"body,omitempty"`
	// Redact uses a pointer so legacy configs without the field receive the
	// privacy-preserving default without forcing a config rewrite.
	Redact *bool `yaml:"redact,omitempty"`
	// Dir is relative to the data root; empty means DefaultLogDir.
	Dir           string `yaml:"dir,omitempty"`
	RetentionDays int    `yaml:"retention_days,omitempty"`
	QuotaBytes    int    `yaml:"quota_bytes,omitempty"`
}

// EnabledValue returns the effective enabled flag (default true).
func (l Logging) EnabledValue() bool {
	if l.Enabled != nil {
		return *l.Enabled
	}
	return true
}

// BodyValue returns whether request and response bodies are persisted
// (default true). The flag is ignored when logging is disabled.
func (l Logging) BodyValue() bool {
	if l.Body != nil {
		return *l.Body
	}
	return true
}

// RedactValue reports whether JSON bodies and stream events are recursively
// redacted before they are persisted. The default is enabled.
func (l Logging) RedactValue() bool {
	if l.Redact != nil {
		return *l.Redact
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

// Provider is a single upstream provider definition. Adapter is the
// default outbound protocol and the protocol used for model discovery
// (docs/v1-scheme.md §5.2). A model may override it.
type Provider struct {
	Name           string            `yaml:"name"`
	Adapter        string            `yaml:"adapter"`
	BaseURL        string            `yaml:"base_url"`
	ModelsURL      string            `yaml:"models_url,omitempty"`
	ExtraHeaders   map[string]string `yaml:"extra_headers,omitempty"`
	DisguiseClient string            `yaml:"disguise_client,omitempty"`
	DefaultModel   string            `yaml:"default_model"`
	Models         []ProviderModel   `yaml:"models,omitempty"`
	Enabled        *bool             `yaml:"enabled,omitempty"`
	SecretRef      string            `yaml:"secret_ref,omitempty"`
	Capabilities   Capabilities      `yaml:"capabilities,omitempty"`
}

func (p Provider) EnabledValue() bool {
	return p.Enabled == nil || *p.Enabled
}

// ProviderModel is one model exposed by a provider. Token limits are zero
// when the upstream model-list response did not publish them. Adapter, when
// set, is the outbound protocol for this model (docs/v1-scheme.md §5.2).
type ProviderModel struct {
	ID              string `yaml:"id"`
	Name            string `yaml:"name,omitempty"`
	Adapter         string `yaml:"adapter,omitempty"`
	Endpoint        string `yaml:"endpoint,omitempty"`
	ContextWindow   int    `yaml:"context_window,omitempty"`
	MaxOutputTokens int    `yaml:"max_output_tokens,omitempty"`
	Enabled         *bool  `yaml:"enabled,omitempty"`
}

func (m ProviderModel) EnabledValue() bool {
	return m.Enabled == nil || *m.Enabled
}

// ModelAdapter returns the outbound adapter for a requested model
// (docs/v1-scheme.md §5.2). A non-empty model-level adapter wins; otherwise
// the provider default is used so catalogs without per-model adapters and
// unpublished prefix overrides stay valid.
func (p Provider) ModelAdapter(modelID string) string {
	for _, model := range p.Models {
		if model.ID != modelID {
			continue
		}
		adapter := strings.TrimSpace(model.Adapter)
		if adapter == endpoint.Custom {
			if wire, ok := endpoint.InferWire(model.Endpoint); ok {
				return wire
			}
			break
		}
		if adapter != "" {
			return adapter
		}
		break
	}
	return p.Adapter
}

// ModelEndpoint returns the user-maintained request path when the model
// uses the custom adapter. Preset Claude and GPT adapters have locked
// paths, so this returns empty for them.
func (p Provider) ModelEndpoint(modelID string) string {
	for _, model := range p.Models {
		if model.ID != modelID {
			continue
		}
		if strings.TrimSpace(model.Adapter) == endpoint.Custom {
			return strings.TrimSpace(model.Endpoint)
		}
		return ""
	}
	return ""
}

// Capabilities describes provider feature flags.
type Capabilities struct {
	ImageInput        bool `yaml:"image_input,omitempty"`
	Reasoning         bool `yaml:"reasoning,omitempty"`
	ContextManagement bool `yaml:"context_management,omitempty"`
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
	c := &Config{
		Version:   1,
		Listen:    Listen{Port: IntPtr(DefaultPort)},
		Logging:   Logging{Enabled: BoolPtr(true), Body: BoolPtr(true), Redact: BoolPtr(true), Dir: DefaultLogDir},
		Limits:    Limits{StreamIdleSeconds: DefaultStreamIdleSeconds},
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
	return c
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
	if c.Limits.StreamIdleSeconds == 0 {
		c.Limits.StreamIdleSeconds = DefaultStreamIdleSeconds
	}
	c.rebuildModelIndex()
}

// ModelOwners returns enabled providers that explicitly declare model. The
// normal path reads the index published with the config; an unindexed config
// (for example one assembled directly in a unit test) computes the same
// result as a compatibility fallback.
func (c *Config) ModelOwners(model string) []string {
	return append([]string(nil), c.modelOwnersView(model)...)
}

// ModelOwnersView returns the immutable provider slice published with the
// config. Read-only hot paths may use it without allocating; callers must not
// mutate the returned slice.
func (c *Config) ModelOwnersView(model string) []string {
	return c.modelOwnersView(model)
}

func (c *Config) modelOwnersView(model string) []string {
	if c == nil {
		return nil
	}
	if c.modelOwners != nil {
		return c.modelOwners[model]
	}
	return c.computeModelOwners(model)
}

func (c *Config) rebuildModelIndex() {
	if c == nil {
		return
	}
	index := make(map[string][]string)
	for id, provider := range c.Providers {
		if !provider.EnabledValue() {
			continue
		}
		for model := range providerDeclaredModels(provider) {
			index[model] = append(index[model], id)
		}
	}
	for model := range index {
		sort.Strings(index[model])
	}
	c.modelOwners = index
}

func (c *Config) computeModelOwners(model string) []string {
	var owners []string
	for id, provider := range c.Providers {
		if provider.EnabledValue() && providerDeclaredModels(provider)[model] {
			owners = append(owners, id)
		}
	}
	sort.Strings(owners)
	return owners
}

func providerDeclaredModels(provider Provider) map[string]bool {
	declared := map[string]bool{}
	if provider.ModelEnabled(provider.DefaultModel) {
		declared[provider.DefaultModel] = true
	}
	for _, model := range provider.Models {
		if model.ID != "" && model.EnabledValue() {
			declared[model.ID] = true
		}
	}
	return declared
}

func (p Provider) ModelEnabled(modelID string) bool {
	if modelID == p.DefaultModel {
		for _, model := range p.Models {
			if model.ID == modelID {
				return model.EnabledValue()
			}
		}
		return true
	}
	for _, model := range p.Models {
		if model.ID == modelID {
			return model.EnabledValue()
		}
	}
	return false
}

// clone returns a deep copy safe to hand out as a snapshot.
func (c *Config) clone() *Config {
	out := *c
	if c.modelOwners != nil {
		out.modelOwners = make(map[string][]string, len(c.modelOwners))
		for model, owners := range c.modelOwners {
			out.modelOwners[model] = append([]string(nil), owners...)
		}
	}
	if c.Listen.Port != nil {
		p := *c.Listen.Port
		out.Listen.Port = &p
	}
	if c.Logging.Enabled != nil {
		b := *c.Logging.Enabled
		out.Logging.Enabled = &b
	}
	if c.Logging.Body != nil {
		b := *c.Logging.Body
		out.Logging.Body = &b
	}
	if c.Logging.Redact != nil {
		b := *c.Logging.Redact
		out.Logging.Redact = &b
	}
	if c.Providers != nil {
		out.Providers = make(map[string]Provider, len(c.Providers))
		for k, v := range c.Providers {
			if v.ExtraHeaders != nil {
				v.ExtraHeaders = make(map[string]string, len(v.ExtraHeaders))
				for name, value := range c.Providers[k].ExtraHeaders {
					v.ExtraHeaders[name] = value
				}
			}
			if v.Models != nil {
				v.Models = append([]ProviderModel(nil), v.Models...)
				for i := range v.Models {
					if v.Models[i].Enabled != nil {
						b := *v.Models[i].Enabled
						v.Models[i].Enabled = &b
					}
				}
			}
			if v.Enabled != nil {
				b := *v.Enabled
				v.Enabled = &b
			}
			out.Providers[k] = v
		}
	}
	if c.Clients.Codex.RemoteCompaction != nil {
		b := *c.Clients.Codex.RemoteCompaction
		out.Clients.Codex.RemoteCompaction = &b
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
