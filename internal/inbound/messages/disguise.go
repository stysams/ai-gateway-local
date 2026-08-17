package messages

import "encoding/json"

// claudeDisguiseThinking is the thinking object sent by Claude Code
// 2.1.228 (docs/v1-scheme.md §20 2026-08-17 body disguise).
var claudeDisguiseThinking = json.RawMessage(`{"type":"adaptive"}`)

var ephemeralCacheControl = json.RawMessage(`{"type":"ephemeral"}`)

// ApplyClaudeDisguise overlays the Messages body fields that Claude Code
// 2.1.228 always sends together with its identity headers. It does not
// replace tools, user messages, or system text, and it never writes
// session metadata (docs/v1-scheme.md §10.1).
func ApplyClaudeDisguise(body []byte, reasoning bool) ([]byte, []string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, nil, err
	}
	if root == nil {
		return nil, nil, nil
	}
	var applied []string
	if reasoning && !presentJSON(root["thinking"]) {
		root["thinking"] = append(json.RawMessage(nil), claudeDisguiseThinking...)
		applied = append(applied, "thinking")
	}
	systemChanged, err := applySystemCacheControl(root)
	if err != nil {
		return nil, nil, err
	}
	if systemChanged {
		applied = append(applied, "system_cache_control")
	}
	if len(applied) == 0 {
		return body, nil, nil
	}
	out, err := json.Marshal(root)
	if err != nil {
		return nil, nil, err
	}
	return out, applied, nil
}

func applySystemCacheControl(root map[string]json.RawMessage) (bool, error) {
	changed := false
	if raw, ok := root["system"]; ok && presentJSON(raw) {
		next, did, err := addCacheControlValue(raw)
		if err != nil {
			return false, err
		}
		if did {
			root["system"] = next
			changed = true
		}
	}
	rawMessages, ok := root["messages"]
	if !ok || !presentJSON(rawMessages) {
		return changed, nil
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return changed, nil
	}
	messagesChanged := false
	for _, message := range messages {
		var role string
		_ = json.Unmarshal(message["role"], &role)
		if role != "system" {
			continue
		}
		content, ok := message["content"]
		if !ok || !presentJSON(content) {
			continue
		}
		next, did, err := addCacheControlValue(content)
		if err != nil {
			return false, err
		}
		if did {
			message["content"] = next
			messagesChanged = true
		}
	}
	if !messagesChanged {
		return changed, nil
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		return false, err
	}
	root["messages"] = encoded
	return true, nil
}

func addCacheControlValue(raw json.RawMessage) (json.RawMessage, bool, error) {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		encoded, err := json.Marshal([]map[string]any{{
			"type":          "text",
			"text":          asString,
			"cache_control": map[string]string{"type": "ephemeral"},
		}})
		if err != nil {
			return nil, false, err
		}
		return encoded, true, nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return raw, false, nil
	}
	changed := false
	out := make([]json.RawMessage, 0, len(parts))
	for _, part := range parts {
		var nested string
		if json.Unmarshal(part, &nested) == nil {
			encoded, err := json.Marshal(map[string]any{
				"type":          "text",
				"text":          nested,
				"cache_control": map[string]string{"type": "ephemeral"},
			})
			if err != nil {
				return nil, false, err
			}
			out = append(out, encoded)
			changed = true
			continue
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(part, &obj) != nil || obj == nil {
			out = append(out, part)
			continue
		}
		var typ string
		_ = json.Unmarshal(obj["type"], &typ)
		if typ != "" && typ != "text" {
			out = append(out, part)
			continue
		}
		if presentJSON(obj["cache_control"]) {
			out = append(out, part)
			continue
		}
		obj["cache_control"] = append(json.RawMessage(nil), ephemeralCacheControl...)
		encoded, err := json.Marshal(obj)
		if err != nil {
			return nil, false, err
		}
		out = append(out, encoded)
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}
