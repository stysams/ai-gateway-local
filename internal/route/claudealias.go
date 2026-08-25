package route

import "ai-gateway/internal/point/clientcatalog"

// ClaudePickerDefault is the picker id for the provider-neutral model.
const ClaudePickerDefault = clientcatalog.ClaudePickerDefault

// ClaudePickerID returns the reversible alias used by Claude-compatible model
// pickers. The alias is presentation-only; Resolve decodes it before routing.
func ClaudePickerID(selectable string) string {
	return clientcatalog.ClaudePickerID(selectable)
}

// DecodeClaudePickerID reverses ClaudePickerID. Non-alias values are unchanged.
func DecodeClaudePickerID(id string) string {
	return clientcatalog.DecodeClaudePickerID(id)
}
