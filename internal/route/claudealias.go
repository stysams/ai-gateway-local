package route

import (
	"strings"
)

// Claude Code's gateway model discovery only keeps /v1/models ids that match
// /(claude|anthropic)/i (docs/v1-scheme.md §7.5, §12.4, §20 2026-08-16).
// /c/claude/v1/models therefore exposes every enabled selectable id as a
// stable, reversible picker alias. The alias is presentation only: Resolve
// decodes it before §7.4, and the upstream still receives the real model name.
//
// Grammar is the OpenCodex readable-alias scheme with this gateway's prefix
// (claude-ocx → claude-gw) so the two products do not collide:
//
//	claude-gw-default                         → gateway-default
//	claude-gw-<provider>--<model>             → provider/model  (plain)
//	claude-gw2-<provider>--<escaped-model>    → provider/model  (/ → ~s, ~ → ~t)
//	claude-gw3-<escaped-full-id>              → full selectable id
//
// v3 is only minted when the provider id cannot be the left side of `--`
// (it contains "--"). Provider ids matching §5.2 almost never need it.

const (
	// ClaudePickerDefault is the picker id for ReservedModel.
	ClaudePickerDefault = "claude-gw-default"

	claudePickerPrefixV1 = "claude-gw-"
	claudePickerPrefixV2 = "claude-gw2-"
	claudePickerPrefixV3 = "claude-gw3-"
)

// ClaudePickerID is the id Claude Code's /model picker will keep. The
// corresponding display_name on /c/claude/v1/models stays the real selectable
// id so the row still reads as gateway-default or <provider-id>/<model-id>.
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

// DecodeClaudePickerID reverses ClaudePickerID. A string that is not a
// well-formed alias is returned unchanged so §7.4 still accepts a raw
// <provider-id>/<model-id> or any other model name.
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
	// Escape tildes first so a slash encoding cannot be confused with a
	// literal "~s" that was already in the model id.
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
