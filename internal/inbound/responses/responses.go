// Package responses is the inbound adapter for the OpenAI Responses API
// (docs/v1-scheme.md task package D): it parses client requests into the
// ir.Request (with same-protocol field preservation) and encodes ir events
// back into non-streaming and SSE Responses responses.
package responses

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ai-gateway/internal/ir"
)

// FieldsRequest keeps the raw bytes of every request field so the
// same-protocol path can rewrite only model/stream and preserve everything
// else (docs/v1-scheme.md §8.3).
type FieldsRequest struct {
	Model  string
	Stream *bool
	Fields map[string]json.RawMessage
}

// InspectFeatures finds image and reasoning inputs without constraining
// unrelated same-protocol fields.
func InspectFeatures(body []byte) ir.RequestFeatures {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return ir.RequestFeatures{}
	}
	features := ir.RequestFeatures{Reasoning: presentJSON(root["reasoning"])}
	var items []map[string]json.RawMessage
	_ = json.Unmarshal(root["input"], &items)
	for _, item := range items {
		var typ string
		_ = json.Unmarshal(item["type"], &typ)
		if typ == "reasoning" {
			features.Reasoning = true
		}
		var parts []struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(item["content"], &parts)
		for _, part := range parts {
			if part.Type == "input_image" || part.Type == "image_url" {
				features.Image = true
			}
		}
	}
	return features
}

// DropReasoning removes the reasoning configuration and reasoning history
// items while preserving all other request fields.
func DropReasoning(body []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	delete(root, "reasoning")
	var items []map[string]json.RawMessage
	if raw, ok := root["input"]; ok && json.Unmarshal(raw, &items) == nil {
		kept := items[:0]
		for _, item := range items {
			var typ string
			_ = json.Unmarshal(item["type"], &typ)
			if typ != "reasoning" {
				kept = append(kept, item)
			}
		}
		encoded, _ := json.Marshal(kept)
		root["input"] = encoded
	}
	return json.Marshal(root)
}

func presentJSON(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}

// Parse parses a Responses request for routing/streaming decisions and
// keeps all fields verbatim.
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

// ParseRequest converts a Responses request into the ir.Request, including
// image inputs and the request-level reasoning configuration.
func ParseRequest(body []byte) (*ir.Request, error) {
	var raw struct {
		Model        string            `json:"model"`
		Stream       *bool             `json:"stream"`
		Instructions string            `json:"instructions"`
		Input        json.RawMessage   `json:"input"`
		Tools        []json.RawMessage `json:"tools"`
		ToolChoice   json.RawMessage   `json:"tool_choice"`
		Reasoning    json.RawMessage   `json:"reasoning"`
		Text         json.RawMessage   `json:"text"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	req := &ir.Request{
		Model:      raw.Model,
		Stream:     raw.Stream != nil && *raw.Stream,
		ToolChoice: normalizeToolChoice(raw.ToolChoice),
	}
	if len(raw.Text) > 0 && string(raw.Text) != "null" {
		var textConfig struct {
			Format struct {
				Type        string          `json:"type"`
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Schema      json.RawMessage `json:"schema"`
				Strict      bool            `json:"strict"`
			} `json:"format"`
		}
		if err := json.Unmarshal(raw.Text, &textConfig); err != nil {
			return nil, fmt.Errorf("invalid field text: %w", err)
		}
		if textConfig.Format.Type != "" {
			if textConfig.Format.Type != "json_schema" || !ir.ValidJSONObject(textConfig.Format.Schema) {
				return nil, fmt.Errorf("%w: text.format must be a json_schema object", ir.ErrUnsupportedContent)
			}
			req.Output = &ir.OutputFormat{Name: textConfig.Format.Name, Description: textConfig.Format.Description, Schema: textConfig.Format.Schema, Strict: textConfig.Format.Strict}
		}
	}
	if len(raw.Reasoning) > 0 && string(raw.Reasoning) != "null" {
		var reasoning struct {
			Effort          string `json:"effort"`
			Summary         string `json:"summary"`
			GenerateSummary string `json:"generate_summary"`
		}
		if err := json.Unmarshal(raw.Reasoning, &reasoning); err != nil {
			return nil, fmt.Errorf("invalid field reasoning: %w", err)
		}
		req.Reasoning = ir.ReasoningConfig{
			Enabled: reasoning.Effort != "none", Effort: reasoning.Effort,
			Summary: reasoning.Summary, Source: ir.ProtocolResponses,
		}
		if req.Reasoning.Summary == "" {
			req.Reasoning.Summary = reasoning.GenerateSummary
		}
	}
	if raw.Instructions != "" {
		req.System = []ir.Block{{Type: ir.BlockText, Text: raw.Instructions}}
	}
	messages, err := parseInput(raw.Input)
	if err != nil {
		return nil, err
	}
	var kept []ir.Message
	for _, message := range messages {
		if message.Role == ir.RoleSystem {
			req.System = append(req.System, message.Content...)
			continue
		}
		kept = append(kept, message)
	}
	req.Messages = kept
	req.Tools, req.DroppedTools, err = parseTools(raw.Tools)
	if err != nil {
		return nil, err
	}
	return req, nil
}

// parseInput converts the Responses input (string or item list) into IR
// messages, flattening tool calls and tool results into their blocks.
func parseInput(raw json.RawMessage) ([]ir.Message, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []ir.Message{{
			Role:    ir.RoleUser,
			Content: []ir.Block{{Type: ir.BlockText, Text: s}},
		}}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	var messages []ir.Message
	for _, itemRaw := range items {
		var item struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			CallID    string          `json:"call_id"`
			Name      string          `json:"name"`
			Arguments string          `json:"arguments"`
			Input     json.RawMessage `json:"input"`
			Output    json.RawMessage `json:"output"`
			Content   json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(itemRaw, &item); err != nil {
			return nil, fmt.Errorf("invalid input item: %w", err)
		}
		switch item.Type {
		case "message":
			var blocks []ir.Block
			if item.Content != nil {
				var parts []struct {
					Type     string `json:"type"`
					Text     string `json:"text"`
					ImageURL string `json:"image_url"`
					Detail   string `json:"detail"`
				}
				if err := json.Unmarshal(item.Content, &parts); err != nil {
					// content 也可能是纯文本（少见）。
					var text string
					if err2 := json.Unmarshal(item.Content, &text); err2 != nil {
						return nil, fmt.Errorf("invalid message content: %w", err)
					}
					blocks = append(blocks, ir.Block{Type: ir.BlockText, Text: text})
				} else {
					for _, p := range parts {
						switch p.Type {
						case "input_text", "output_text":
							// output_text is the official Responses replay
							// form of a previous assistant turn
							// (docs/v1-scheme.md §8.1, §20 2026-08-16).
							blocks = append(blocks, ir.Block{Type: ir.BlockText, Text: p.Text})
						case "input_image", "image_url":
							image, err := ir.ParseImageURL(p.ImageURL)
							if err != nil {
								return nil, fmt.Errorf("invalid input_image: %w", err)
							}
							image.Detail = p.Detail
							blocks = append(blocks, ir.Block{Type: ir.BlockImage, Image: image})
						default:
							return nil, fmt.Errorf("%w: content type %q", ir.ErrUnsupportedContent, p.Type)
						}
					}
				}
			}
			role, err := normalizeInputRole(item.Role)
			if err != nil {
				return nil, err
			}
			messages = append(messages, ir.Message{Role: role, Content: blocks})
		case "function_call":
			if item.CallID == "" || item.Name == "" {
				return nil, fmt.Errorf("invalid function_call item: missing call_id or name")
			}
			messages = append(messages, ir.Message{
				Role: ir.RoleAssistant,
				Content: []ir.Block{{
					Type: ir.BlockToolCall,
					ToolCall: &ir.ToolCall{
						ID:        item.CallID,
						Name:      item.Name,
						Arguments: json.RawMessage(item.Arguments),
					},
				}},
			})
		case "custom_tool_call":
			if item.CallID == "" || item.Name == "" {
				return nil, fmt.Errorf("invalid custom_tool_call item: missing call_id or name")
			}
			messages = append(messages, ir.Message{
				Role: ir.RoleAssistant,
				Content: []ir.Block{{
					Type: ir.BlockToolCall,
					ToolCall: &ir.ToolCall{
						ID:        item.CallID,
						Name:      item.Name,
						Arguments: ir.WrapFreeformInput(rawJSONText(item.Input)),
						Custom:    true,
					},
				}},
			})
		case "function_call_output", "custom_tool_call_output":
			if item.CallID == "" {
				return nil, fmt.Errorf("invalid %s item: missing call_id", item.Type)
			}
			messages = append(messages, ir.Message{
				Role: ir.RoleTool,
				Content: []ir.Block{{
					Type:       ir.BlockToolResult,
					ToolResult: &ir.ToolResult{ID: item.CallID, Content: rawJSONText(item.Output)},
				}},
			})
		case "reasoning":
			var reasoning struct {
				EncryptedContent string `json:"encrypted_content"`
				Summary          []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"summary"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(itemRaw, &reasoning); err != nil {
				return nil, fmt.Errorf("invalid reasoning item: %w", err)
			}
			if reasoning.EncryptedContent != "" {
				return nil, fmt.Errorf("%w: encrypted reasoning cannot be converted across protocols", ir.ErrUnsupportedContent)
			}
			var text strings.Builder
			for _, part := range reasoning.Summary {
				text.WriteString(part.Text)
			}
			for _, part := range reasoning.Content {
				text.WriteString(part.Text)
			}
			messages = append(messages, ir.Message{Role: ir.RoleAssistant, Content: []ir.Block{{
				Type: ir.BlockReasoning, Reasoning: &ir.Reasoning{Text: text.String()},
			}}})
		default:
			return nil, fmt.Errorf("%w: input item type %q", ir.ErrUnsupportedContent, item.Type)
		}
	}
	return messages, nil
}

// parseTools converts Responses function, custom, and namespace tools into
// IR tools. Hosted provider-executed tools are recorded as DroppedTool
// instead of failing the request (docs/v1-scheme.md §8.4).
func parseTools(rawTools []json.RawMessage) ([]ir.Tool, []ir.DroppedTool, error) {
	var tools []ir.Tool
	var dropped []ir.DroppedTool
	for _, raw := range rawTools {
		more, drop, err := parseOneTool(raw)
		if err != nil {
			return nil, nil, err
		}
		tools = append(tools, more...)
		dropped = append(dropped, drop...)
	}
	return tools, dropped, nil
}

func parseOneTool(raw json.RawMessage) ([]ir.Tool, []ir.DroppedTool, error) {
	var t struct {
		Type        string            `json:"type"`
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Parameters  json.RawMessage   `json:"parameters"`
		Strict      bool              `json:"strict"`
		Format      json.RawMessage   `json:"format"`
		Tools       []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, nil, fmt.Errorf("invalid tool definition: %w", err)
	}
	switch t.Type {
	case "function":
		if t.Name == "" {
			return nil, nil, fmt.Errorf("%w: function tool is missing name", ir.ErrUnsupportedContent)
		}
		return []ir.Tool{{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  ir.SchemaOrEmpty(t.Parameters),
			Strict:      t.Strict,
		}}, nil, nil
	case "custom":
		if t.Name == "" {
			return nil, nil, fmt.Errorf("%w: custom tool is missing name", ir.ErrUnsupportedContent)
		}
		var dropped []ir.DroppedTool
		if presentJSON(t.Format) {
			dropped = append(dropped, ir.DroppedTool{
				Type:   "custom_format",
				Name:   t.Name,
				Reason: "custom tool grammar or text format cannot be expressed as a JSON schema",
			})
		}
		return []ir.Tool{{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  ir.SchemaOrFreeform(t.Parameters),
			Custom:      true,
			Strict:      t.Strict,
		}}, dropped, nil
	case "namespace":
		var tools []ir.Tool
		var dropped []ir.DroppedTool
		for _, nested := range t.Tools {
			more, drop, err := parseOneTool(nested)
			if err != nil {
				return nil, nil, fmt.Errorf("namespace %q: %w", t.Name, err)
			}
			tools = append(tools, more...)
			dropped = append(dropped, drop...)
		}
		return tools, dropped, nil
	default:
		if hostedToolType(t.Type) {
			return nil, []ir.DroppedTool{{
				Type:   t.Type,
				Name:   t.Name,
				Reason: "hosted tool cannot be converted across protocols",
			}}, nil
		}
		return nil, nil, fmt.Errorf("%w: tool type %q is not convertible", ir.ErrUnsupportedContent, t.Type)
	}
}

func hostedToolType(typ string) bool {
	switch typ {
	case "web_search", "web_search_preview", "file_search", "code_interpreter",
		"computer", "computer_use", "computer_use_preview", "image_generation", "mcp":
		return true
	default:
		return false
	}
}

func normalizeInputRole(role string) (ir.Role, error) {
	switch role {
	case "", "user":
		return ir.RoleUser, nil
	case "assistant":
		return ir.RoleAssistant, nil
	case "system", "developer":
		return ir.RoleSystem, nil
	default:
		return "", fmt.Errorf("invalid message role %q", role)
	}
}

func rawJSONText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return string(raw)
}

// normalizeToolChoice accepts the Responses forms (already the IR
// canonical shape).
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
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &named); err == nil && named.Type == "function" && named.Name != "" {
		return raw
	}
	return nil
}

// WriteError writes the OpenAI-native error envelope.
func WriteError(w http.ResponseWriter, status int, message, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
			"code":    code,
		},
	})
}

const respIDPrefix = "resp_gateway"

// EncodeNonStream renders an aggregated ir.Response as a non-streaming
// Response document.
func EncodeNonStream(w io.Writer, model string, resp *ir.Response) error {
	var output []any
	if resp.Reasoning != "" {
		output = append(output, map[string]any{
			"id": "rs_gateway", "type": "reasoning", "status": "completed",
			"summary": []any{map[string]any{"type": "summary_text", "text": resp.Reasoning}},
		})
	}
	if resp.Text != "" {
		output = append(output, map[string]any{
			"id":      "msg_gateway",
			"type":    "message",
			"role":    "assistant",
			"status":  "completed",
			"content": []any{map[string]any{"type": "output_text", "text": resp.Text, "annotations": []any{}}},
		})
	}
	for _, tc := range resp.ToolCalls {
		if tc.Custom {
			output = append(output, map[string]any{
				"id":      "ctc_" + tc.ID,
				"type":    "custom_tool_call",
				"call_id": tc.ID,
				"name":    tc.Name,
				"input":   ir.UnwrapFreeformInput(tc.Arguments),
				"status":  "completed",
			})
			continue
		}
		output = append(output, map[string]any{
			"id":        "fc_" + tc.ID,
			"type":      "function_call",
			"call_id":   tc.ID,
			"name":      tc.Name,
			"arguments": string(tc.Arguments),
			"status":    "completed",
		})
	}
	out := map[string]any{
		"id":         respIDPrefix,
		"object":     "response",
		"created_at": float64(time.Now().Unix()),
		"status":     "completed",
		"model":      model,
		"output":     output,
	}
	if resp.Usage.TotalTokens > 0 || resp.Usage.InputTokens > 0 {
		usage := map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"total_tokens":  resp.Usage.TotalTokens,
		}
		if resp.Usage.CacheReadInputTokens > 0 {
			usage["input_tokens_details"] = map[string]any{"cached_tokens": resp.Usage.CacheReadInputTokens}
		}
		out["usage"] = usage
	}
	data, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("encode responses body: %w", err)
	}
	_, err = w.Write(data)
	return err
}

// EncodeStream renders ir events as Responses SSE events, flushing after
// every event. response.completed ends the stream; an upstream failure is
// emitted as response.failed and never followed by a success event.
//
// Every data payload carries "type" (and sequence_number) so clients that
// dispatch on the JSON type field — including Codex Desktop — can see
// response.completed. Open items are closed before that terminal event.
func EncodeStream(w io.Writer, flush func(), model string, next func() (ir.Event, error)) error {
	enc := &streamEncoder{w: w, flush: flush, model: model}
	for {
		ev, err := next()
		if err == io.EOF {
			if !enc.terminal {
				// docs/v1-scheme.md §8.2: a broken stream ends in a protocol
				// error event, never a fabricated completion.
				return enc.failed("upstream stream ended before response.completed")
			}
			return nil
		}
		if err != nil {
			return err
		}
		switch ev.Type {
		case ir.EventStarted:
			if err := enc.emit("response.created", map[string]any{
				"response": map[string]any{"id": respIDPrefix, "object": "response", "status": "in_progress", "model": model},
			}); err != nil {
				return err
			}
		case ir.EventTextDelta:
			if err := enc.ensureText(); err != nil {
				return err
			}
			enc.text.WriteString(ev.Text)
			if err := enc.emit("response.output_text.delta", map[string]any{
				"item_id": enc.textID, "output_index": enc.textIx, "content_index": 0, "delta": ev.Text,
			}); err != nil {
				return err
			}
		case ir.EventReasoningDelta:
			if err := enc.ensureReasoning(); err != nil {
				return err
			}
			enc.reasoning.WriteString(ev.Text)
			if err := enc.emit("response.reasoning_summary_text.delta", map[string]any{
				"item_id": enc.reasoningID, "output_index": enc.reasoningIx, "summary_index": 0, "delta": ev.Text,
			}); err != nil {
				return err
			}
		case ir.EventToolCallStarted:
			enc.itemN++
			item := functionCallItem(ev, "", "in_progress")
			if ev.ToolCustom {
				item = customToolCallItem(ev, "", "in_progress")
			}
			if err := enc.emit("response.output_item.added", map[string]any{
				"output_index": enc.itemN - 1,
				"item":         item,
			}); err != nil {
				return err
			}
		case ir.EventToolCallArgumentsDlt:
			if ev.ToolCustom {
				// JSON-wrapped freeform deltas are not the custom-tool text
				// stream; the completed event carries the unwrapped input.
				continue
			}
			if err := enc.emit("response.function_call_arguments.delta", map[string]any{
				"item_id": "fc_" + ev.ToolCallID, "output_index": 0, "delta": ev.ArgumentsDelta,
			}); err != nil {
				return err
			}
		case ir.EventToolCallCompleted:
			if ev.ToolCustom {
				input := ir.UnwrapFreeformInput(json.RawMessage(ev.Arguments))
				if err := enc.emit("response.custom_tool_call_input.done", map[string]any{
					"item_id": "ctc_" + ev.ToolCallID, "output_index": 0, "input": input,
				}); err != nil {
					return err
				}
				if err := enc.emit("response.output_item.done", map[string]any{
					"output_index": 0,
					"item":         customToolCallItem(ev, input, "completed"),
				}); err != nil {
					return err
				}
				continue
			}
			if err := enc.emit("response.function_call_arguments.done", map[string]any{
				"item_id": "fc_" + ev.ToolCallID, "output_index": 0,
				"arguments": ev.Arguments,
			}); err != nil {
				return err
			}
			if err := enc.emit("response.output_item.done", map[string]any{
				"output_index": 0,
				"item":         functionCallItem(ev, ev.Arguments, "completed"),
			}); err != nil {
				return err
			}
		case ir.EventTextCompleted, ir.EventReasoningCompleted, ir.EventUsage:
			// 客户端流式协议无需这些事件。
		case ir.EventCompleted:
			return enc.completed()
		case ir.EventError:
			msg := "upstream error"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			return enc.failed(msg)
		}
	}
}

type streamEncoder struct {
	w           io.Writer
	flush       func()
	model       string
	seq         int
	itemN       int
	textID      string
	textIx      int
	text        strings.Builder
	reasoningID string
	reasoningIx int
	reasoning   strings.Builder
	terminal    bool
}

func (e *streamEncoder) emit(typ string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["type"] = typ
	payload["sequence_number"] = e.seq
	e.seq++
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(e.w, "event: "+typ+"\ndata: "+string(data)+"\n\n"); err != nil {
		return err
	}
	e.flush()
	return nil
}

func (e *streamEncoder) ensureText() error {
	if e.textID != "" {
		return nil
	}
	e.itemN++
	e.textIx = e.itemN - 1
	e.textID = fmt.Sprintf("msg_%d", e.itemN)
	if err := e.emit("response.output_item.added", map[string]any{
		"output_index": e.textIx,
		"item": map[string]any{
			"id": e.textID, "type": "message", "role": "assistant",
			"content": []any{}, "status": "in_progress",
		},
	}); err != nil {
		return err
	}
	return e.emit("response.content_part.added", map[string]any{
		"item_id": e.textID, "output_index": e.textIx, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})
}

func (e *streamEncoder) ensureReasoning() error {
	if e.reasoningID != "" {
		return nil
	}
	e.itemN++
	e.reasoningIx = e.itemN - 1
	e.reasoningID = fmt.Sprintf("rs_%d", e.itemN)
	if err := e.emit("response.output_item.added", map[string]any{
		"output_index": e.reasoningIx,
		"item":         map[string]any{"id": e.reasoningID, "type": "reasoning", "summary": []any{}, "status": "in_progress"},
	}); err != nil {
		return err
	}
	return e.emit("response.reasoning_summary_part.added", map[string]any{
		"item_id": e.reasoningID, "output_index": e.reasoningIx, "summary_index": 0,
		"part": map[string]any{"type": "summary_text", "text": ""},
	})
}

func (e *streamEncoder) closeText() error {
	if e.textID == "" {
		return nil
	}
	text := e.text.String()
	if err := e.emit("response.output_text.done", map[string]any{
		"item_id": e.textID, "output_index": e.textIx, "content_index": 0, "text": text,
	}); err != nil {
		return err
	}
	if err := e.emit("response.content_part.done", map[string]any{
		"item_id": e.textID, "output_index": e.textIx, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}},
	}); err != nil {
		return err
	}
	if err := e.emit("response.output_item.done", map[string]any{
		"output_index": e.textIx,
		"item": map[string]any{
			"id": e.textID, "type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
		},
	}); err != nil {
		return err
	}
	e.textID = ""
	return nil
}

func (e *streamEncoder) closeReasoning() error {
	if e.reasoningID == "" {
		return nil
	}
	text := e.reasoning.String()
	if err := e.emit("response.reasoning_summary_text.done", map[string]any{
		"item_id": e.reasoningID, "output_index": e.reasoningIx, "summary_index": 0, "text": text,
	}); err != nil {
		return err
	}
	if err := e.emit("response.reasoning_summary_part.done", map[string]any{
		"item_id": e.reasoningID, "output_index": e.reasoningIx, "summary_index": 0,
		"part": map[string]any{"type": "summary_text", "text": text},
	}); err != nil {
		return err
	}
	if err := e.emit("response.output_item.done", map[string]any{
		"output_index": e.reasoningIx,
		"item": map[string]any{
			"id": e.reasoningID, "type": "reasoning", "status": "completed",
			"summary": []any{map[string]any{"type": "summary_text", "text": text}},
		},
	}); err != nil {
		return err
	}
	e.reasoningID = ""
	return nil
}

func (e *streamEncoder) closeOpenItems() error {
	if e.reasoningID != "" && (e.textID == "" || e.reasoningIx < e.textIx) {
		if err := e.closeReasoning(); err != nil {
			return err
		}
	}
	if err := e.closeText(); err != nil {
		return err
	}
	return e.closeReasoning()
}

func (e *streamEncoder) completed() error {
	if err := e.closeOpenItems(); err != nil {
		return err
	}
	if err := e.emit("response.completed", map[string]any{
		"response": map[string]any{
			"id": respIDPrefix, "object": "response", "status": "completed", "model": e.model,
			"output": []any{}, "parallel_tool_calls": true,
		},
	}); err != nil {
		return err
	}
	e.terminal = true
	return nil
}

func (e *streamEncoder) failed(msg string) error {
	if err := e.emit("response.failed", map[string]any{
		"response": map[string]any{
			"id": respIDPrefix, "object": "response", "status": "failed", "model": e.model,
			"error": map[string]any{"code": "upstream_error", "message": msg},
		},
	}); err != nil {
		return err
	}
	e.terminal = true
	return nil
}

func functionCallItem(ev ir.Event, arguments, status string) map[string]any {
	return map[string]any{
		"id": "fc_" + ev.ToolCallID, "type": "function_call", "call_id": ev.ToolCallID,
		"name": ev.ToolName, "arguments": arguments, "status": status,
	}
}

func customToolCallItem(ev ir.Event, input, status string) map[string]any {
	return map[string]any{
		"id": "ctc_" + ev.ToolCallID, "type": "custom_tool_call", "call_id": ev.ToolCallID,
		"name": ev.ToolName, "input": input, "status": status,
	}
}
