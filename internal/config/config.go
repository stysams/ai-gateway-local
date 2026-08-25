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
	modelRefs   map[string][]ModelRef
}

// ModelRef identifies a model without losing its credential-group owner.
type ModelRef struct {
	Provider string
	KeyID    string
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
	Name string `yaml:"name"`
	// KeyGroups is the new credential boundary. Each group owns one plain
	// API key, its model catalog, and its effective endpoint defaults.
	KeyGroups      map[string]KeyGroup `yaml:"key_groups,omitempty"`
	BaseURL        string              `yaml:"base_url"`
	ModelsURL      string              `yaml:"models_url,omitempty"`
	ExtraHeaders   map[string]string   `yaml:"extra_headers,omitempty"`
	DisguiseClient string              `yaml:"disguise_client,omitempty"`
	Enabled        *bool               `yaml:"enabled,omitempty"`
	Capabilities   Capabilities        `yaml:"capabilities,omitempty"`

	// Legacy fields remain for in-memory compatibility with older fixtures.
	// New configuration writes use KeyGroups and the management API does not
	// expose these fields.
	Adapter      string          `yaml:"-"`
	DefaultModel string          `yaml:"-"`
	Models       []ProviderModel `yaml:"-"`
	SecretRef    string          `yaml:"-"`
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

// KeyGroup is a provider-scoped credential and model collection. APIKey is
// intentionally persisted in config.yaml under the approved next-stage
// security contract.
type KeyGroup struct {
	Name         string          `yaml:"name"`
	Enabled      *bool           `yaml:"enabled,omitempty"`
	APIKey       string          `yaml:"api_key,omitempty"`
	Endpoint     string          `yaml:"endpoint,omitempty"`
	Adapter      string          `yaml:"adapter,omitempty"`
	DefaultModel string          `yaml:"default_model"`
	Models       []ProviderModel `yaml:"models,omitempty"`
}

// KeyGroup returns a provider key group by its stable identifier.
func (p Provider) KeyGroup(keyID string) (KeyGroup, bool) {
	group, ok := p.KeyGroups[keyID]
	if ok && p.hasLegacyRuntimeFields() {
		if strings.TrimSpace(p.Adapter) != "" {
			group.Adapter = p.Adapter
		}
		if strings.TrimSpace(p.DefaultModel) != "" {
			group.DefaultModel = p.DefaultModel
		}
		if p.Models != nil {
			group.Models = append([]ProviderModel(nil), p.Models...)
		}
	}
	return group, ok
}

// hasLegacyRuntimeFields is limited to direct in-memory fixtures. Parsed YAML
// and management payloads reject these fields before they reach runtime.
func (p Provider) hasLegacyRuntimeFields() bool {
	return strings.TrimSpace(p.Adapter) != "" || strings.TrimSpace(p.DefaultModel) != "" || p.Models != nil
}

// KeyGroupModelAdapter resolves a model protocol inside one key group.
func (p Provider) KeyGroupModelAdapter(keyID, modelID string) string {
	group, ok := p.KeyGroup(keyID)
	if !ok {
		return ""
	}
	return group.ModelAdapter(modelID)
}

// EnabledValue returns the effective key-group availability.
func (g KeyGroup) EnabledValue() bool {
	return g.Enabled == nil || *g.Enabled
}

// Model returns one model declared by the key group.
func (g KeyGroup) Model(modelID string) (ProviderModel, bool) {
	for _, model := range g.Models {
		if model.ID == modelID {
			return model, true
		}
	}
	return ProviderModel{}, false
}

// EffectiveEndpoint returns the model endpoint, falling back to the group
// endpoint. New configurations require one of these values for every model.
func (g KeyGroup) EffectiveEndpoint(modelID string) string {
	if model, ok := g.Model(modelID); ok && strings.TrimSpace(model.Endpoint) != "" {
		return strings.TrimSpace(model.Endpoint)
	}
	return strings.TrimSpace(g.Endpoint)
}

// ModelAdapter resolves the protocol from an explicit adapter or the
// effective endpoint suffix. Custom adapters still require a known wire
// suffix so the gateway never guesses from provider or model names.
func (g KeyGroup) ModelAdapter(modelID string) string {
	model, ok := g.Model(modelID)
	if ok && strings.TrimSpace(model.Adapter) != "" && model.Adapter != endpoint.Custom {
		return strings.TrimSpace(model.Adapter)
	}
	if adapter, ok := endpoint.InferWire(g.EffectiveEndpoint(modelID)); ok {
		return adapter
	}
	if ok && strings.TrimSpace(model.Adapter) == endpoint.Custom {
		return endpoint.Custom
	}
	return strings.TrimSpace(g.Adapter)
}

// ModelEnabled reports whether a declared model can be selected.
func (g KeyGroup) ModelEnabled(modelID string) bool {
	model, ok := g.Model(modelID)
	return ok && model.EnabledValue()
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

// Routes contains the required client routes and the optional Claude Desktop route.
type Routes struct {
	Codex         Route `yaml:"codex"`
	Claude        Route `yaml:"claude"`
	ClaudeDesktop Route `yaml:"claude_desktop,omitempty"`
	Grok          Route `yaml:"grok"`
	Generic       Route `yaml:"generic"`
}

// Route points one client at one provider/key-group/model triple.
type Route struct {
	Provider string `yaml:"provider"`
	KeyID    string `yaml:"key_id"`
	Model    string `yaml:"model"`
}

// route returns the route for one of the four fixed client ids.
func (r Routes) route(name string) Route {
	switch name {
	case "codex":
		return r.Codex
	case "claude":
		return r.Claude
	case "claude_desktop":
		return r.ClaudeDesktop
	case "grok":
		return r.Grok
	case "generic":
		return r.Generic
	}
	return Route{}
}

// Defaults returns the initial configuration generated when no config file
// exists. The default groups are keyless so the gateway can start before a
// user configures an authenticated provider.
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
				Name:    "OpenRouter",
				BaseURL: "https://openrouter.ai/api/v1",
				KeyGroups: map[string]KeyGroup{
					"default": {
						Name:         "默认密钥",
						DefaultModel: "anthropic/claude-sonnet-4",
						Models:       []ProviderModel{{ID: "anthropic/claude-sonnet-4", Endpoint: "/chat/completions"}},
					},
				},
				Capabilities: Capabilities{ImageInput: true, Reasoning: true},
			},
			"ollama": {
				Name:    "Ollama",
				BaseURL: "http://127.0.0.1:11434/v1",
				KeyGroups: map[string]KeyGroup{
					"default": {
						Name:         "默认密钥",
						DefaultModel: "qwen3",
						Models:       []ProviderModel{{ID: "qwen3", Endpoint: "/chat/completions"}},
					},
				},
			},
		},
		Routes: Routes{
			ClaudeDesktop: Route{Provider: "openrouter", KeyID: "default", Model: "anthropic/claude-sonnet-4"},
			Codex:         Route{Provider: "openrouter", KeyID: "default", Model: "anthropic/claude-sonnet-4"},
			Claude:        Route{Provider: "openrouter", KeyID: "default", Model: "anthropic/claude-sonnet-4"},
			Grok:          Route{Provider: "openrouter", KeyID: "default", Model: "anthropic/claude-sonnet-4"},
			Generic:       Route{Provider: "ollama", KeyID: "default", Model: "qwen3"},
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

// ModelRefs returns enabled provider/key-group owners for a model.
func (c *Config) ModelRefs(model string) []ModelRef {
	return append([]ModelRef(nil), c.modelRefsView(model)...)
}

// ModelRefsView returns the immutable owner slice published with the config.
func (c *Config) ModelRefsView(model string) []ModelRef {
	return c.modelRefsView(model)
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

func (c *Config) modelRefsView(model string) []ModelRef {
	if c == nil {
		return nil
	}
	if c.modelRefs != nil {
		return c.modelRefs[model]
	}
	return c.computeModelRefs(model)
}

func (c *Config) rebuildModelIndex() {
	if c == nil {
		return
	}
	index := make(map[string][]string)
	refs := make(map[string][]ModelRef)
	for id, provider := range c.Providers {
		if !provider.EnabledValue() {
			continue
		}
		for keyID := range provider.KeyGroups {
			group, _ := provider.KeyGroup(keyID)
			if !group.EnabledValue() {
				continue
			}
			for model := range keyGroupDeclaredModels(group) {
				index[model] = append(index[model], id)
				refs[model] = append(refs[model], ModelRef{Provider: id, KeyID: keyID})
			}
		}
		// Keep the legacy in-memory index for tests and callers that assemble
		// pre-key-group configs directly. Parsed new configs use KeyGroups.
		for model := range providerDeclaredModels(provider) {
			if len(provider.KeyGroups) == 0 {
				index[model] = append(index[model], id)
				refs[model] = append(refs[model], ModelRef{Provider: id})
			}
		}
	}
	for model := range index {
		sort.Strings(index[model])
		sort.Slice(refs[model], func(i, j int) bool {
			if refs[model][i].Provider == refs[model][j].Provider {
				return refs[model][i].KeyID < refs[model][j].KeyID
			}
			return refs[model][i].Provider < refs[model][j].Provider
		})
	}
	c.modelOwners = index
	c.modelRefs = refs
}

func (c *Config) computeModelOwners(model string) []string {
	var owners []string
	for id, provider := range c.Providers {
		if !provider.EnabledValue() {
			continue
		}
		if len(provider.KeyGroups) > 0 {
			for keyID := range provider.KeyGroups {
				group, _ := provider.KeyGroup(keyID)
				if group.EnabledValue() && keyGroupDeclaredModels(group)[model] {
					owners = append(owners, id)
					break
				}
			}
		} else if providerDeclaredModels(provider)[model] {
			owners = append(owners, id)
		}
	}
	sort.Strings(owners)
	return owners
}

func (c *Config) computeModelRefs(model string) []ModelRef {
	var refs []ModelRef
	for id, provider := range c.Providers {
		if !provider.EnabledValue() {
			continue
		}
		if len(provider.KeyGroups) == 0 {
			if providerDeclaredModels(provider)[model] {
				refs = append(refs, ModelRef{Provider: id})
			}
			continue
		}
		for keyID := range provider.KeyGroups {
			group, _ := provider.KeyGroup(keyID)
			if group.EnabledValue() && keyGroupDeclaredModels(group)[model] {
				refs = append(refs, ModelRef{Provider: id, KeyID: keyID})
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Provider == refs[j].Provider {
			return refs[i].KeyID < refs[j].KeyID
		}
		return refs[i].Provider < refs[j].Provider
	})
	return refs
}

func keyGroupDeclaredModels(group KeyGroup) map[string]bool {
	declared := map[string]bool{}
	if group.ModelEnabled(group.DefaultModel) {
		declared[group.DefaultModel] = true
	}
	for _, model := range group.Models {
		if model.ID != "" && model.EnabledValue() {
			declared[model.ID] = true
		}
	}
	return declared
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
	if c.modelRefs != nil {
		out.modelRefs = make(map[string][]ModelRef, len(c.modelRefs))
		for model, refs := range c.modelRefs {
			out.modelRefs[model] = append([]ModelRef(nil), refs...)
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
			if v.KeyGroups != nil {
				v.KeyGroups = make(map[string]KeyGroup, len(v.KeyGroups))
				for keyID, group := range c.Providers[k].KeyGroups {
					if group.Models != nil {
						group.Models = append([]ProviderModel(nil), group.Models...)
						for i := range group.Models {
							if group.Models[i].Enabled != nil {
								b := *group.Models[i].Enabled
								group.Models[i].Enabled = &b
							}
						}
					}
					if group.Enabled != nil {
						b := *group.Enabled
						group.Enabled = &b
					}
					v.KeyGroups[keyID] = group
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
