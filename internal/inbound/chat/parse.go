package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"ai-gateway/internal/ir"
)

// InspectFeatures finds capability-relevant content without rejecting or
// rewriting unrelated same-protocol extension fields.
func InspectFeatures(body []byte) ir.RequestFeatures {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return ir.RequestFeatures{}
	}
	features := ir.RequestFeatures{Reasoning: presentJSON(root["reasoning_effort"])}
	var messages []map[string]json.RawMessage
	_ = json.Unmarshal(root["messages"], &messages)
	for _, message := range messages {
		if presentJSON(message["reasoning_content"]) {
			features.Reasoning = true
		}
		var parts []struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(message["content"], &parts)
		for _, part := range parts {
			switch part.Type {
			case "image_url", "input_image":
				features.Image = true
			case "reasoning", "thinking":
				features.Reasoning = true
			}
		}
	}
	return features
}

// DropReasoning removes request-level and message-level reasoning fields
// while preserving every unrelated JSON field.
func DropReasoning(body []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	delete(root, "reasoning_effort")
	var messages []map[string]json.RawMessage
	if raw, ok := root["messages"]; ok && json.Unmarshal(raw, &messages) == nil {
		for _, message := range messages {
			delete(message, "reasoning_content")
			var parts []map[string]json.RawMessage
			if rawContent, ok := message["content"]; ok && json.Unmarshal(rawContent, &parts) == nil {
				kept := parts[:0]
				for _, part := range parts {
					var typ string
					_ = json.Unmarshal(part["type"], &typ)
					if typ != "reasoning" && typ != "thinking" {
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

func presentJSON(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}

// ParseRequest parses a chat/completions body into the protocol-independent
// ir.Request. Text, images, reasoning settings, system messages, tool
// definitions, tool_choice, tool calls and tool results are converted.
func ParseRequest(body []byte) (*ir.Request, error) {
	var raw struct {
		Model      string            `json:"model"`
		Stream     *bool             `json:"stream"`
		Messages   []json.RawMessage `json:"messages"`
		Tools      []json.RawMessage `json:"tools"`
		ToolChoice json.RawMessage   `json:"tool_choice"`
		Reasoning  json.RawMessage   `json:"reasoning_effort"`
		Output     json.RawMessage   `json:"response_format"`
		Other      map[string]json.RawMessage
	}
	// 未知字段收集：先解到 map 再摘取已知字段。
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	raw.Other = map[string]json.RawMessage{}
	for k, v := range fields {
		switch k {
		case "model", "stream", "messages", "tools", "tool_choice", "reasoning_effort", "response_format":
		default:
			raw.Other[k] = v
		}
	}
	// 重新解码已知字段（复用 raw 结构）。
	if m, ok := fields["model"]; ok {
		if err := json.Unmarshal(m, &raw.Model); err != nil {
			return nil, fmt.Errorf("invalid field model: %w", err)
		}
	}
	if s, ok := fields["stream"]; ok {
		var b bool
		if err := json.Unmarshal(s, &b); err != nil {
			return nil, fmt.Errorf("invalid field stream: %w", err)
		}
		raw.Stream = &b
	}
	if msgs, ok := fields["messages"]; ok {
		if err := json.Unmarshal(msgs, &raw.Messages); err != nil {
			return nil, fmt.Errorf("invalid field messages: %w", err)
		}
	}
	if tools, ok := fields["tools"]; ok {
		if err := json.Unmarshal(tools, &raw.Tools); err != nil {
			return nil, fmt.Errorf("invalid field tools: %w", err)
		}
	}
	if tc, ok := fields["tool_choice"]; ok {
		raw.ToolChoice = tc
	}
	if reasoning, ok := fields["reasoning_effort"]; ok {
		raw.Reasoning = reasoning
	}
	if output, ok := fields["response_format"]; ok {
		raw.Output = output
	}

	req := &ir.Request{
		Model:      raw.Model,
		Stream:     raw.Stream != nil && *raw.Stream,
		ToolChoice: normalizeToolChoice(raw.ToolChoice),
		Extensions: raw.Other,
	}
	if len(raw.Output) > 0 && string(raw.Output) != "null" {
		var format struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Schema      json.RawMessage `json:"schema"`
				Strict      bool            `json:"strict"`
			} `json:"json_schema"`
		}
		if err := json.Unmarshal(raw.Output, &format); err != nil {
			return nil, fmt.Errorf("invalid field response_format: %w", err)
		}
		if format.Type != "json_schema" || !ir.ValidJSONObject(format.JSONSchema.Schema) {
			return nil, fmt.Errorf("%w: response_format must be a json_schema object", ir.ErrUnsupportedContent)
		}
		req.Output = &ir.OutputFormat{Name: format.JSONSchema.Name, Description: format.JSONSchema.Description, Schema: format.JSONSchema.Schema, Strict: format.JSONSchema.Strict}
	}
	if len(raw.Reasoning) > 0 && string(raw.Reasoning) != "null" {
		if err := json.Unmarshal(raw.Reasoning, &req.Reasoning.Effort); err != nil {
			return nil, fmt.Errorf("invalid field reasoning_effort: %w", err)
		}
		req.Reasoning.Enabled = req.Reasoning.Effort != "none"
		req.Reasoning.Source = ir.ProtocolChat
	}
	var err error
	req.System, req.Messages, err = parseMessages(raw.Messages)
	if err != nil {
		return nil, err
	}
	req.Tools, err = parseTools(raw.Tools)
	if err != nil {
		return nil, err
	}
	return req, nil
}

// parseMessages converts chat messages into IR messages. Chat system
// messages become IR system blocks; tool role messages carry tool results;
// assistant messages may carry text and tool calls.
func parseMessages(rawMsgs []json.RawMessage) ([]ir.Block, []ir.Message, error) {
	var system []ir.Block
	var messages []ir.Message
	for _, raw := range rawMsgs {
		var m struct {
			Role             string            `json:"role"`
			Content          json.RawMessage   `json:"content"`
			ReasoningContent string            `json:"reasoning_content"`
			ToolCallID       string            `json:"tool_call_id"`
			ToolCalls        []json.RawMessage `json:"tool_calls"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, nil, fmt.Errorf("invalid message: %w", err)
		}
		switch m.Role {
		case "system":
			text, err := contentText(m.Content)
			if err != nil {
				return nil, nil, err
			}
			system = append(system, ir.Block{Type: ir.BlockText, Text: text})
			continue
		case "tool":
			text, err := contentText(m.Content)
			if err != nil {
				return nil, nil, err
			}
			messages = append(messages, ir.Message{
				Role: ir.RoleTool,
				Content: []ir.Block{{
					Type:       ir.BlockToolResult,
					ToolResult: &ir.ToolResult{ID: m.ToolCallID, Content: text},
				}},
			})
			continue
		case "user", "assistant":
		default:
			return nil, nil, fmt.Errorf("invalid message role %q", m.Role)
		}
		blocks, err := contentBlocks(m.Content, m.Role)
		if err != nil {
			return nil, nil, err
		}
		if m.ReasoningContent != "" {
			blocks = append(blocks, ir.Block{Type: ir.BlockReasoning, Reasoning: &ir.Reasoning{Text: m.ReasoningContent}})
		}
		for _, rawTC := range m.ToolCalls {
			var tc struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}
			if err := json.Unmarshal(rawTC, &tc); err != nil {
				return nil, nil, fmt.Errorf("invalid tool call: %w", err)
			}
			if tc.ID == "" {
				return nil, nil, fmt.Errorf("invalid tool call: missing id")
			}
			blocks = append(blocks, ir.Block{
				Type: ir.BlockToolCall,
				ToolCall: &ir.ToolCall{
					ID:   tc.ID,
					Name: tc.Function.Name,
					// chat 的 arguments 是 JSON 字符串：解包为原始 JSON。
					Arguments: json.RawMessage(tc.Function.Arguments),
				},
			})
		}
		messages = append(messages, ir.Message{Role: ir.Role(m.Role), Content: blocks})
	}
	return system, messages, nil
}

// contentText extracts plain text from a chat content field (string or
// text blocks).
func contentText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	blocks, err := contentBlocks(raw, "user")
	if err != nil {
		return "", err
	}
	var out string
	for _, b := range blocks {
		if b.Type == ir.BlockText {
			out += b.Text
		}
	}
	return out, nil
}

// contentBlocks converts a chat content field (string or block array) into
// IR blocks. A null content (legal for assistant tool-call messages) yields
// no blocks.
func contentBlocks(raw json.RawMessage, role string) ([]ir.Block, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []ir.Block{{Type: ir.BlockText, Text: s}}, nil
	}
	var arr []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		ImageURL json.RawMessage `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("invalid message content: %w", err)
	}
	var blocks []ir.Block
	for _, part := range arr {
		switch part.Type {
		case "text":
			blocks = append(blocks, ir.Block{Type: ir.BlockText, Text: part.Text})
		case "image_url", "input_image":
			image, err := parseImage(part.ImageURL)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, ir.Block{Type: ir.BlockImage, Image: image})
		case "reasoning", "thinking":
			text := part.Text
			if text == "" {
				text = part.Thinking
			}
			blocks = append(blocks, ir.Block{Type: ir.BlockReasoning, Reasoning: &ir.Reasoning{Text: text}})
		default:
			return nil, fmt.Errorf("%w: content type %q", ir.ErrUnsupportedContent, part.Type)
		}
	}
	return blocks, nil
}

func parseImage(raw json.RawMessage) (*ir.Image, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		var obj struct {
			URL    string `json:"url"`
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, fmt.Errorf("invalid image_url: %w", err)
		}
		value = obj.URL
		image, err := ir.ParseImageURL(value)
		if err != nil {
			return nil, fmt.Errorf("invalid image_url: %w", err)
		}
		image.Detail = obj.Detail
		return image, nil
	}
	image, err := ir.ParseImageURL(value)
	if err != nil {
		return nil, fmt.Errorf("invalid image_url: %w", err)
	}
	return image, nil
}

// parseTools converts chat tool definitions into IR tools.
func parseTools(rawTools []json.RawMessage) ([]ir.Tool, error) {
	var tools []ir.Tool
	for _, raw := range rawTools {
		var t struct {
			Type     string `json:"type"`
			Function struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
				Strict      bool            `json:"strict"`
			} `json:"function"`
		}
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("invalid tool definition: %w", err)
		}
		if t.Type != "function" || t.Function.Name == "" {
			return nil, fmt.Errorf("%w: only function tools are supported", ir.ErrUnsupportedContent)
		}
		tools = append(tools, ir.Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
			Strict:      t.Function.Strict,
		})
	}
	return tools, nil
}

// normalizeToolChoice maps the chat tool_choice forms onto the IR canonical
// form: "auto" | "none" | "required" | {"type":"function","name":...}.
func normalizeToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "auto", "none", "required":
			return raw
		}
		return nil
	}
	var named struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &named); err == nil && named.Type == "function" && named.Function.Name != "" {
		out, _ := json.Marshal(map[string]any{"type": "function", "name": named.Function.Name})
		return out
	}
	return nil
}
