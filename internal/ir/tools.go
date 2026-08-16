package ir

import (
	"bytes"
	"encoding/json"
	"strings"
)

// EmptyObjectSchema is a valid JSON Schema object with no properties.
var EmptyObjectSchema = json.RawMessage(`{"type":"object","properties":{}}`)

// FreeformInputSchema wraps a custom/freeform tool as a single string field
// so JSON-only protocols can carry the raw input without inventing a
// grammar (docs/v1-scheme.md §8.4).
var FreeformInputSchema = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string","description":"Raw freeform tool input."}},"required":["input"]}`)

// SchemaOrEmpty returns raw when it is a non-empty JSON value, otherwise a
// cloned empty object schema. Callers must not append to the result of a
// fallback clone.
func SchemaOrEmpty(raw json.RawMessage) json.RawMessage {
	if isEmptyJSON(raw) {
		return cloneRaw(EmptyObjectSchema)
	}
	return raw
}

// SchemaOrFreeform returns raw when it is a non-empty JSON value, otherwise
// a cloned freeform input schema.
func SchemaOrFreeform(raw json.RawMessage) json.RawMessage {
	if isEmptyJSON(raw) {
		return cloneRaw(FreeformInputSchema)
	}
	return raw
}

// WrapFreeformInput stores a custom tool's raw text as {"input":"..."}.
func WrapFreeformInput(input string) json.RawMessage {
	raw, err := json.Marshal(map[string]string{"input": input})
	if err != nil {
		return cloneRaw(json.RawMessage(`{"input":""}`))
	}
	return raw
}

// UnwrapFreeformInput recovers the raw custom-tool text from a wrapped
// {"input":"..."} object. Non-wrapper JSON is returned as text.
func UnwrapFreeformInput(args json.RawMessage) string {
	if isEmptyJSON(args) {
		return ""
	}
	var asString string
	if err := json.Unmarshal(args, &asString); err == nil {
		return asString
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(args, &keys); err != nil {
		return string(args)
	}
	if len(keys) == 1 {
		if raw, ok := keys["input"]; ok {
			var input string
			if err := json.Unmarshal(raw, &input); err == nil {
				return input
			}
		}
	}
	return string(args)
}

// CustomToolNames returns the set of custom/freeform tool names.
func CustomToolNames(tools []Tool) map[string]bool {
	names := make(map[string]bool)
	for _, tool := range tools {
		if tool.Custom && tool.Name != "" {
			names[tool.Name] = true
		}
	}
	return names
}

// MarkCustomToolEvent sets ToolCustom when the event belongs to a custom tool.
func MarkCustomToolEvent(ev Event, customNames, customIDs map[string]bool) Event {
	if len(customNames) == 0 && len(customIDs) == 0 {
		return ev
	}
	switch ev.Type {
	case EventToolCallStarted:
		if customNames[ev.ToolName] {
			ev.ToolCustom = true
			if ev.ToolCallID != "" && customIDs != nil {
				customIDs[ev.ToolCallID] = true
			}
		}
	case EventToolCallArgumentsDlt, EventToolCallCompleted:
		if customIDs[ev.ToolCallID] || customNames[ev.ToolName] {
			ev.ToolCustom = true
		}
	}
	return ev
}

// MarkCustomToolCalls flags accumulated tool calls whose names are custom.
func MarkCustomToolCalls(calls []ToolCall, customNames map[string]bool) {
	if len(customNames) == 0 {
		return
	}
	for i := range calls {
		if customNames[calls[i].Name] {
			calls[i].Custom = true
		}
	}
}

func isEmptyJSON(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value == "" || value == "null"
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

// ValidJSONObject reports whether raw is a JSON object.
func ValidJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var obj map[string]json.RawMessage
	return json.Unmarshal(trimmed, &obj) == nil
}
