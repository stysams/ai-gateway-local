// Package messages is the inbound adapter for the Anthropic Messages API
// (docs/v1-scheme.md task package D): it parses client requests into the
// ir.Request (with same-protocol field preservation) and encodes ir events
// back into non-streaming and SSE Messages responses.
package messages

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"ai-gateway/internal/ir"
)

// FieldsRequest keeps the raw bytes of every request field so the
// same-protocol path rewrites only model/stream.
type FieldsRequest struct {
	Model  string
	Stream *bool
	Fields map[string]json.RawMessage
}

// InspectFeatures finds image and thinking content without constraining
// unrelated same-protocol fields.
func InspectFeatures(body []byte) ir.RequestFeatures {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return ir.RequestFeatures{}
	}
	features := ir.RequestFeatures{Reasoning: presentJSON(root["thinking"])}
	var messages []map[string]json.RawMessage
	_ = json.Unmarshal(root["messages"], &messages)
	for _, message := range messages {
		var parts []struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(message["content"], &parts)
		for _, part := range parts {
			switch part.Type {
			case "image":
				features.Image = true
			case "thinking", "redacted_thinking":
				features.Reasoning = true
			}
		}
	}
	return features
}

// DropReasoning removes the thinking configuration and thinking history
// blocks while preserving every unrelated field.
func DropReasoning(body []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	delete(root, "thinking")
	var messages []map[string]json.RawMessage
	if raw, ok := root["messages"]; ok && json.Unmarshal(raw, &messages) == nil {
		for _, message := range messages {
			var parts []map[string]json.RawMessage
			if rawContent, ok := message["content"]; ok && json.Unmarshal(rawContent, &parts) == nil {
				kept := parts[:0]
				for _, part := range parts {
					var typ string
					_ = json.Unmarshal(part["type"], &typ)
					if typ != "thinking" && typ != "redacted_thinking" {
						kept = append(kept, part)
					}
				}
				encoded, _ := json.Marshal(kept)
				message["content"] = encoded
			}
		}
		encoded, _ := json.Marshal(messages)
		root["messages"] = encoded
	}
	return json.Marshal(root)
}

// DropContextManagement removes the Anthropic context-management extension
// when the selected upstream does not implement it. The field is deliberately
// handled independently from the IR because it is a provider capability, not
// a conversation semantic that can be converted across protocols.
func DropContextManagement(body []byte) ([]byte, bool, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, false, err
	}
	if _, ok := root["context_management"]; !ok {
		return body, false, nil
	}
	delete(root, "context_management")
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}

func presentJSON(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}

// Parse parses a Messages request for routing/streaming decisions.
func Parse(body []byte) (*FieldsRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	if fields == nil {
		return nil, fmt.Errorf("invalid JSON body: expected an object")
	}
	req := &FieldsRequest{Fields: fields}
	if raw, ok := fields["model"]; ok {
		if err := json.Unmarshal(raw, &req.Model); err != nil {
			return nil, fmt.Errorf("invalid field model: %w", err)
		}
	}
	if raw, ok := fields["stream"]; ok {
		var s bool
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("invalid field stream: %w", err)
		}
		req.Stream = &s
	}
	return req, nil
}

// StreamValue returns the effective streaming flag.
func (r *FieldsRequest) StreamValue() bool {
	if r.Stream != nil {
		return *r.Stream
	}
	return false
}

// Rewrite replaces model and stream, preserving all other fields.
func (r *FieldsRequest) Rewrite(model string, stream bool) ([]byte, error) {
	m, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	r.Fields["model"] = m
	s, err := json.Marshal(stream)
	if err != nil {
		return nil, err
	}
	r.Fields["stream"] = s
	return json.Marshal(r.Fields)
}

// ParseRequest converts a Messages request into the ir.Request. System,
// images, thinking, messages, tools and tool_choice are converted.
func ParseRequest(body []byte) (*ir.Request, error) {
	var raw struct {
		Model      string            `json:"model"`
		Stream     *bool             `json:"stream"`
		System     json.RawMessage   `json:"system"`
		Messages   []json.RawMessage `json:"messages"`
		Tools      []json.RawMessage `json:"tools"`
		ToolChoice json.RawMessage   `json:"tool_choice"`
		Thinking   json.RawMessage   `json:"thinking"`
		MaxTokens  int64             `json:"max_tokens"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	req := &ir.Request{
		Model:      raw.Model,
		Stream:     raw.Stream != nil && *raw.Stream,
		ToolChoice: normalizeToolChoice(raw.ToolChoice),
	}
	if len(raw.Thinking) > 0 && string(raw.Thinking) != "null" {
		var thinking struct {
			Type         string `json:"type"`
			BudgetTokens int64  `json:"budget_tokens"`
			Display      string `json:"display"`
		}
		if err := json.Unmarshal(raw.Thinking, &thinking); err != nil {
			return nil, fmt.Errorf("invalid field thinking: %w", err)
		}
		req.Reasoning = ir.ReasoningConfig{
			Enabled: thinking.Type != "disabled", Type: thinking.Type,
			BudgetTokens: thinking.BudgetTokens, Display: thinking.Display,
			Source: ir.ProtocolMessages,
		}
	}
	// system：字符串或 text 块列表。
	if len(raw.System) > 0 {
		blocks, err := parseSystemContent(raw.System, "system")
		if err != nil {
			return nil, err
		}
		req.System = append(req.System, blocks...)
	}
	messageSystem, messages, err := parseMessages(raw.Messages)
	if err != nil {
		return nil, err
	}
	req.System = append(req.System, messageSystem...)
	req.Messages = messages
	req.Tools, err = parseTools(raw.Tools)
	if err != nil {
		return nil, err
	}
	return req, nil
}

// parseMessages converts Messages content blocks into IR messages. Claude
// Code may place deferred-tool guidance in a system-role message even though
// the public Messages shape normally uses the top-level system field. Those
// messages join the IR system blocks so cross-protocol routing preserves them.
//
// Claude Code puts tool_result blocks on a user message and may mix them
// with later text (ToolSearch emits a tool_reference part plus "Tool
// loaded."). Cross-protocol adapters need RoleTool for pairing, so each
// tool_result becomes its own tool message; leftover user blocks stay
// RoleUser (docs/v1-scheme.md §8.1, §10, §20 2026-08-16).
func parseMessages(rawMsgs []json.RawMessage) ([]ir.Block, []ir.Message, error) {
	var system []ir.Block
	var messages []ir.Message
	for _, raw := range rawMsgs {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, nil, fmt.Errorf("invalid message: %w", err)
		}
		switch m.Role {
		case "system":
			blocks, err := parseSystemContent(m.Content, "system message content")
			if err != nil {
				return nil, nil, err
			}
			system = append(system, blocks...)
			continue
		case "user", "assistant":
		default:
			return nil, nil, fmt.Errorf("invalid message role %q", m.Role)
		}
		var text string
		if err := json.Unmarshal(m.Content, &text); err == nil {
			messages = append(messages, ir.Message{
				Role:    ir.Role(m.Role),
				Content: []ir.Block{{Type: ir.BlockText, Text: text}},
			})
			continue
		}
		var blocks []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
			Source    json.RawMessage `json:"source"`
			Thinking  string          `json:"thinking"`
			Signature string          `json:"signature"`
			Data      string          `json:"data"`
		}
		if err := json.Unmarshal(m.Content, &blocks); err != nil {
			return nil, nil, fmt.Errorf("invalid message content: %w", err)
		}
		var content []ir.Block
		for _, b := range blocks {
			switch b.Type {
			case "text":
				content = append(content, ir.Block{Type: ir.BlockText, Text: b.Text})
			case "tool_use":
				if b.ID == "" || b.Name == "" {
					return nil, nil, fmt.Errorf("invalid tool_use block: missing id or name")
				}
				content = append(content, ir.Block{
					Type:     ir.BlockToolCall,
					ToolCall: &ir.ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Input},
				})
			case "tool_result":
				content = append(content, ir.Block{
					Type:       ir.BlockToolResult,
					ToolResult: &ir.ToolResult{ID: b.ToolUseID, IsError: b.IsError, Content: parseToolResultContent(b.Content)},
				})
			case "image":
				image, err := parseImageSource(b.Source)
				if err != nil {
					return nil, nil, err
				}
				content = append(content, ir.Block{Type: ir.BlockImage, Image: image})
			case "thinking":
				content = append(content, ir.Block{Type: ir.BlockReasoning, Reasoning: &ir.Reasoning{
					Text: b.Thinking, Signature: b.Signature,
				}})
			case "redacted_thinking":
				content = append(content, ir.Block{Type: ir.BlockReasoning, Reasoning: &ir.Reasoning{
					Encrypted: b.Data,
				}})
			default:
				return nil, nil, fmt.Errorf("%w: content block type %q", ir.ErrUnsupportedContent, b.Type)
			}
		}
		role := ir.Role(m.Role)
		if role == ir.RoleUser {
			messages = append(messages, splitUserToolResults(content)...)
			continue
		}
		messages = append(messages, ir.Message{Role: role, Content: content})
	}
	return system, messages, nil
}

// splitUserToolResults turns Claude Code's mixed user turn into IR tool
// messages followed by any leftover user blocks. Responses and Chat both
// require the result to sit next to its call, not inside a user text item.
func splitUserToolResults(content []ir.Block) []ir.Message {
	var out []ir.Message
	var pending []ir.Block
	flushUser := func() {
		if len(pending) == 0 {
			return
		}
		out = append(out, ir.Message{Role: ir.RoleUser, Content: pending})
		pending = nil
	}
	for _, block := range content {
		if block.Type == ir.BlockToolResult {
			flushUser()
			out = append(out, ir.Message{Role: ir.RoleTool, Content: []ir.Block{block}})
			continue
		}
		pending = append(pending, block)
	}
	flushUser()
	return out
}

// parseToolResultContent keeps a string or all-text array as text. Mixed or
// structured parts (Claude Code tool_reference) stay as the original JSON
// so the model still sees what loaded. Empty / null content is "".
func parseToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		allText := true
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				sb.WriteString(p.Text)
				continue
			}
			if p.Type != "" {
				allText = false
			}
		}
		if allText {
			return sb.String()
		}
	}
	return string(raw)
}

// parseSystemContent accepts the two text-only system forms used by the
// Messages protocol and Claude Code. Additional block properties such as
// cache_control are intentionally ignored, but non-text blocks are rejected
// because cross-protocol conversion cannot preserve their semantics.
func parseSystemContent(raw json.RawMessage, field string) ([]ir.Block, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []ir.Block{{Type: ir.BlockText, Text: text}}, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", field, err)
	}
	result := make([]ir.Block, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			return nil, fmt.Errorf("%w: %s block type %q", ir.ErrUnsupportedContent, field, block.Type)
		}
		result = append(result, ir.Block{Type: ir.BlockText, Text: block.Text})
	}
	return result, nil
}

func parseImageSource(raw json.RawMessage) (*ir.Image, error) {
	var source struct {
		Type      string `json:"type"`
		URL       string `json:"url"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("invalid image source: %w", err)
	}
	switch source.Type {
	case "url":
		image, err := ir.ParseImageURL(source.URL)
		if err != nil {
			return nil, fmt.Errorf("invalid image source: %w", err)
		}
		return image, nil
	case "base64":
		image, err := ir.ParseImageURL("data:" + source.MediaType + ";base64," + source.Data)
		if err != nil {
			return nil, fmt.Errorf("invalid image source: %w", err)
		}
		return image, nil
	default:
		return nil, fmt.Errorf("%w: image source type %q", ir.ErrUnsupportedContent, source.Type)
	}
}

// parseTools converts Messages tool definitions into IR tools.
func parseTools(rawTools []json.RawMessage) ([]ir.Tool, error) {
	var tools []ir.Tool
	for _, raw := range rawTools {
		var t struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		}
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("invalid tool definition: %w", err)
		}
		if t.Name == "" {
			return nil, fmt.Errorf("invalid tool definition: missing name")
		}
		tools = append(tools, ir.Tool{Name: t.Name, Description: t.Description, Parameters: t.InputSchema})
	}
	return tools, nil
}

// normalizeToolChoice maps Messages tool_choice onto the IR canonical form.
// "any" has no chat/responses equivalent and is rejected by the caller.
func normalizeToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "auto", "none":
			return raw
		case "any":
			out, _ := json.Marshal("required")
			return out
		}
		return nil
	}
	var named struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &named); err != nil || named.Type == "" {
		return nil
	}
	switch named.Type {
	case "auto", "none":
		out, _ := json.Marshal(named.Type)
		return out
	case "any":
		out, _ := json.Marshal("required")
		return out
	case "tool":
		if named.Name == "" {
			return nil
		}
		out, _ := json.Marshal(map[string]any{"type": "function", "name": named.Name})
		return out
	}
	return nil
}

// WriteError writes the Anthropic-native error envelope.
func WriteError(w http.ResponseWriter, status int, message, typ string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":    typ,
		"message": message,
	})
}

const msgIDPrefix = "msg_gateway"

// EncodeNonStream renders an aggregated ir.Response as a non-streaming
// Message document.
func EncodeNonStream(w io.Writer, model string, resp *ir.Response) error {
	var content []any
	if resp.Reasoning != "" {
		content = append(content, map[string]any{"type": "thinking", "thinking": resp.Reasoning, "signature": ""})
	}
	if resp.Text != "" {
		content = append(content, map[string]any{"type": "text", "text": resp.Text})
	}
	for _, tc := range resp.ToolCalls {
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Name,
			"input": json.RawMessage(tc.Arguments),
		})
	}
	out := map[string]any{
		"id":            msgIDPrefix,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   stopReason(resp.StopReason),
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
		},
	}
	data, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("encode messages body: %w", err)
	}
	_, err = w.Write(data)
	return err
}

// stopReason maps IR stop reasons onto the Messages protocol's set.
func stopReason(stop string) string {
	switch stop {
	case "", "stop":
		return "end_turn"
	case "tool_calls", "tool_use":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "completed":
		return "end_turn"
	}
	return stop
}

// EncodeStream renders ir events as Messages SSE events, flushing after
// every event. message_stop ends the stream; an upstream failure is emitted
// as an error event and never followed by message_stop.
func EncodeStream(w io.Writer, flush func(), model string, next func() (ir.Event, error)) error {
	blockIndex := 0
	toolBlocks := map[string]int{} // call_id -> block index
	emitted := false
	for {
		ev, err := next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch ev.Type {
		case ir.EventStarted:
			msg := map[string]any{
				"id": msgIDPrefix, "type": "message", "role": "assistant", "model": model,
				"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
			}
			if err := writeSSE(w, "message_start", map[string]any{"message": msg}); err != nil {
				return err
			}
			flush()
			emitted = true
		case ir.EventTextDelta:
			idx := blockIndex
			blockIndex++
			if err := writeSSE(w, "content_block_start", map[string]any{
				"index":         idx,
				"content_block": map[string]any{"type": "text", "text": ""},
			}); err != nil {
				return err
			}
			flush()
			if err := writeSSE(w, "content_block_delta", map[string]any{
				"index": idx,
				"delta": map[string]any{"type": "text_delta", "text": ev.Text},
			}); err != nil {
				return err
			}
			flush()
			if err := writeSSE(w, "content_block_stop", map[string]any{"index": idx}); err != nil {
				return err
			}
			flush()
		case ir.EventReasoningDelta:
			idx := blockIndex
			blockIndex++
			if err := writeSSE(w, "content_block_start", map[string]any{
				"index":         idx,
				"content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""},
			}); err != nil {
				return err
			}
			flush()
			if err := writeSSE(w, "content_block_delta", map[string]any{
				"index": idx,
				"delta": map[string]any{"type": "thinking_delta", "thinking": ev.Text},
			}); err != nil {
				return err
			}
			flush()
			if err := writeSSE(w, "content_block_stop", map[string]any{"index": idx}); err != nil {
				return err
			}
			flush()
		case ir.EventToolCallStarted:
			idx := blockIndex
			blockIndex++
			toolBlocks[ev.ToolCallID] = idx
			if err := writeSSE(w, "content_block_start", map[string]any{
				"index": idx,
				"content_block": map[string]any{
					"type": "tool_use", "id": ev.ToolCallID, "name": ev.ToolName, "input": map[string]any{},
				},
			}); err != nil {
				return err
			}
			flush()
		case ir.EventToolCallArgumentsDlt:
			idx, ok := toolBlocks[ev.ToolCallID]
			if !ok {
				continue
			}
			if err := writeSSE(w, "content_block_delta", map[string]any{
				"index": idx,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": ev.ArgumentsDelta},
			}); err != nil {
				return err
			}
			flush()
		case ir.EventToolCallCompleted:
			idx, ok := toolBlocks[ev.ToolCallID]
			if !ok {
				continue
			}
			if err := writeSSE(w, "content_block_stop", map[string]any{"index": idx}); err != nil {
				return err
			}
			flush()
		case ir.EventTextCompleted, ir.EventReasoningCompleted, ir.EventUsage:
			// 客户端流式协议无需这些事件。
		case ir.EventCompleted:
			if err := writeSSE(w, "message_delta", map[string]any{
				"delta": map[string]any{"stop_reason": stopReason(ev.StopReason), "stop_sequence": nil},
				"usage": map[string]any{"output_tokens": 0},
			}); err != nil {
				return err
			}
			flush()
			if err := writeSSE(w, "message_stop", map[string]any{}); err != nil {
				return err
			}
			flush()
			return nil
		case ir.EventError:
			msg := "upstream error"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			if err := writeSSE(w, "error", map[string]any{"type": "api_error", "message": msg}); err != nil {
				return err
			}
			flush()
			return nil
		}
	}
	// 上游断流：没有 message_stop 就不伪装完成。若已开始则发错误事件。
	if emitted {
		if err := writeSSE(w, "error", map[string]any{
			"type": "api_error", "message": "upstream stream ended unexpectedly",
		}); err != nil {
			return err
		}
		flush()
	}
	return nil
}

func writeSSE(w io.Writer, typ string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "event: "+typ+"\ndata: "+string(data)+"\n\n")
	return err
}
