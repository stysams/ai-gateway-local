// Package clientcatalog carries the model information a point adapter writes
// into a client configuration file. It is a leaf package so the adapters can
// share the type without importing internal/point (docs/v1-scheme.md §7.3).
package clientcatalog

import "strings"

// ReservedModel is the provider-neutral model name every pointed client may
// use as its startup model; the gateway resolves it against the client's
// current route per request (§7.3). It must stay equal to route.ReservedModel —
// this package is a leaf so it cannot import internal/route, and
// TestReservedModelNamesAgree in internal/server guards the two against drift.
const ReservedModel = "gateway-default"

// ReservedDisplayName is the picker label for the reserved model. §7.5 makes the
// model id the display-name fallback and the §12.5 example writes it verbatim,
// so the reserved model shows as its own id.
const ReservedDisplayName = ReservedModel

const (
	ClaudePickerDefault  = "claude-gw-default"
	claudePickerPrefixV1 = "claude-gw-"
	claudePickerPrefixV2 = "claude-gw2-"
	claudePickerPrefixV3 = "claude-gw3-"
)

// ClaudePickerID returns the reversible alias used by Claude-compatible model
// pickers. The alias is presentation-only; the gateway decodes it before routing.
func ClaudePickerID(selectable string) string {
	if selectable == "" || selectable == ReservedModel {
		return ClaudePickerDefault
	}
	provider, model, ok := strings.Cut(selectable, "/")
	if !ok || provider == "" || model == "" || strings.Contains(provider, "--") {
		return claudePickerPrefixV3 + encodeClaudePickerPart(selectable)
	}
	if strings.ContainsAny(model, "/~") {
		return claudePickerPrefixV2 + provider + "--" + encodeClaudePickerPart(model)
	}
	return claudePickerPrefixV1 + provider + "--" + model
}

// DecodeClaudePickerID reverses ClaudePickerID and leaves non-alias values alone.
func DecodeClaudePickerID(id string) string {
	if id == "" || id == ClaudePickerDefault {
		if id == ClaudePickerDefault {
			return ReservedModel
		}
		return id
	}
	if rest, ok := strings.CutPrefix(id, claudePickerPrefixV2); ok {
		provider, model, ok := splitClaudePicker(rest)
		if !ok {
			return id
		}
		model = decodeClaudePickerPart(model)
		if model == "" {
			return id
		}
		return provider + "/" + model
	}
	if rest, ok := strings.CutPrefix(id, claudePickerPrefixV3); ok {
		if rest == "" {
			return id
		}
		return decodeClaudePickerPart(rest)
	}
	if rest, ok := strings.CutPrefix(id, claudePickerPrefixV1); ok {
		provider, model, ok := splitClaudePicker(rest)
		if !ok {
			return id
		}
		return provider + "/" + model
	}
	return id
}

func splitClaudePicker(rest string) (provider, model string, ok bool) {
	provider, model, ok = strings.Cut(rest, "--")
	if !ok || provider == "" || model == "" {
		return "", "", false
	}
	return provider, model, true
}

func encodeClaudePickerPart(s string) string {
	s = strings.ReplaceAll(s, "~", "~t")
	return strings.ReplaceAll(s, "/", "~s")
}

func decodeClaudePickerPart(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '~' && i+1 < len(s) {
			switch s[i+1] {
			case 's':
				b.WriteByte('/')
				i++
				continue
			case 't':
				b.WriteByte('~')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Entry is one selectable gateway model. ID is already in the
// `<provider-id>/<model-id>` form the gateway resolves per §7.4.
type Entry struct {
	ID          string
	DisplayName string
}

// Label is the name shown in a client's model picker. Client pickers always
// display the selectable id (`<provider-id>/<model-id>`), so this returns ID.
func (e Entry) Label() string {
	if e.ID != "" {
		return e.ID
	}
	return e.DisplayName
}

// Settings is what a client configuration must express after point.
//
// PreferredModel is the client's startup model only; per §7.3 it never limits
// which models the user can pick inside the agent. Catalog is the complete set
// of enabled models and is only written for clients that can hold a list
// natively — see the 2026-08-15 verification record in §20.
// RemoteCompaction is Codex-only: Claude and Grok ignore it.
type Settings struct {
	PreferredModel   string
	Catalog          []Entry
	RemoteCompaction bool
	SubagentModel    string
	TitleModel       string
	RouteDisplayName string
}

// Model returns the model name a client should start with. An unset preferred
// model means "follow the route", which is exactly what ReservedModel encodes.
func (s Settings) Model() string {
	if s.PreferredModel != "" {
		return s.PreferredModel
	}
	return ReservedModel
}

func (s Settings) SubagentModelValue() string {
	if s.SubagentModel != "" {
		return s.SubagentModel
	}
	return ReservedModel
}

func (s Settings) TitleModelValue() string {
	if s.TitleModel != "" {
		return s.TitleModel
	}
	return ReservedModel
}

// Equal reports whether two settings would produce the same client configuration.
func (s Settings) Equal(other Settings) bool {
	if s.PreferredModel != other.PreferredModel || s.RemoteCompaction != other.RemoteCompaction || s.SubagentModel != other.SubagentModel || s.TitleModel != other.TitleModel || s.RouteDisplayName != other.RouteDisplayName || len(s.Catalog) != len(other.Catalog) {
		return false
	}
	for i, entry := range s.Catalog {
		if entry != other.Catalog[i] {
			return false
		}
	}
	return true
}
