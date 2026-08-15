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
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	req := &ir.Request{
		Model:      raw.Model,
		Stream:     raw.Stream != nil && *raw.Stream,
		ToolChoice: normalizeToolChoice(raw.ToolChoice),
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
	req.Messages = messages
	req.Tools, err = parseTools(raw.Tools)
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
			Output    string          `json:"output"`
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
						case "input_text":
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
			role := ir.Role(item.Role)
			if role == "" {
				role = ir.RoleUser
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
		case "function_call_output":
			if item.CallID == "" {
				return nil, fmt.Errorf("invalid function_call_output item: missing call_id")
			}
			messages = append(messages, ir.Message{
				Role: ir.RoleTool,
				Content: []ir.Block{{
					Type:       ir.BlockToolResult,
					ToolResult: &ir.ToolResult{ID: item.CallID, Content: item.Output},
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

// parseTools converts Responses function tools into IR tools.
func parseTools(rawTools []json.RawMessage) ([]ir.Tool, error) {
	var tools []ir.Tool
	for _, raw := range rawTools {
		var t struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("invalid tool definition: %w", err)
		}
		if t.Type != "function" || t.Name == "" {
			return nil, fmt.Errorf("%w: only function tools are supported", ir.ErrUnsupportedContent)
		}
		tools = append(tools, ir.Tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	return tools, nil
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
		out["usage"] = map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"total_tokens":  resp.Usage.TotalTokens,
		}
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
func EncodeStream(w io.Writer, flush func(), model string, next func() (ir.Event, error)) error {
	itemIndex := 0
	textItems := map[string]bool{}
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
			if err := writeSSE(w, "response.created", map[string]any{
				"response": map[string]any{"id": respIDPrefix, "object": "response", "status": "in_progress", "model": model},
			}); err != nil {
				return err
			}
			flush()
		case ir.EventTextDelta:
			itemIndex++
			itemID := fmt.Sprintf("msg_%d", itemIndex)
			textItems[itemID] = true
			if err := writeSSE(w, "response.output_item.added", map[string]any{
				"output_index": itemIndex - 1,
				"item": map[string]any{
					"id": itemID, "type": "message", "role": "assistant",
					"content": []any{}, "status": "in_progress",
				},
			}); err != nil {
				return err
			}
			flush()
			if err := writeSSE(w, "response.content_part.added", map[string]any{
				"item_id": itemID, "output_index": itemIndex - 1, "content_index": 0,
				"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
			}); err != nil {
				return err
			}
			flush()
			if err := writeSSE(w, "response.output_text.delta", map[string]any{
				"item_id": itemID, "output_index": itemIndex - 1, "content_index": 0, "delta": ev.Text,
			}); err != nil {
				return err
			}
			flush()
		case ir.EventReasoningDelta:
			itemIndex++
			itemID := fmt.Sprintf("rs_%d", itemIndex)
			if err := writeSSE(w, "response.output_item.added", map[string]any{
				"output_index": itemIndex - 1,
				"item":         map[string]any{"id": itemID, "type": "reasoning", "summary": []any{}, "status": "in_progress"},
			}); err != nil {
				return err
			}
			flush()
			if err := writeSSE(w, "response.reasoning_summary_text.delta", map[string]any{
				"item_id": itemID, "output_index": itemIndex - 1, "summary_index": 0, "delta": ev.Text,
			}); err != nil {
				return err
			}
			flush()
		case ir.EventToolCallStarted:
			itemIndex++
			if err := writeSSE(w, "response.output_item.added", map[string]any{
				"output_index": itemIndex - 1,
				"item": map[string]any{
					"id": "fc_" + ev.ToolCallID, "type": "function_call", "call_id": ev.ToolCallID,
					"name": ev.ToolName, "arguments": "", "status": "in_progress",
				},
			}); err != nil {
				return err
			}
			flush()
		case ir.EventToolCallArgumentsDlt:
			if err := writeSSE(w, "response.function_call_arguments.delta", map[string]any{
				"item_id": "fc_" + ev.ToolCallID, "output_index": 0, "delta": ev.ArgumentsDelta,
			}); err != nil {
				return err
			}
			flush()
		case ir.EventToolCallCompleted:
			if err := writeSSE(w, "response.function_call_arguments.done", map[string]any{
				"item_id": "fc_" + ev.ToolCallID, "output_index": 0,
				"arguments": ev.Arguments,
			}); err != nil {
				return err
			}
			flush()
			if err := writeSSE(w, "response.output_item.done", map[string]any{
				"output_index": 0,
				"item": map[string]any{
					"id": "fc_" + ev.ToolCallID, "type": "function_call", "call_id": ev.ToolCallID,
					"name": ev.ToolName, "arguments": ev.Arguments, "status": "completed",
				},
			}); err != nil {
				return err
			}
			flush()
		case ir.EventTextCompleted, ir.EventReasoningCompleted, ir.EventUsage:
			// 客户端流式协议无需这些事件。
		case ir.EventCompleted:
			if err := writeSSE(w, "response.completed", map[string]any{
				"response": map[string]any{
					"id": respIDPrefix, "object": "response", "status": "completed", "model": model,
					"output": []any{}, "parallel_tool_calls": true,
				},
			}); err != nil {
				return err
			}
			flush()
			return nil
		case ir.EventError:
			msg := "upstream error"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			if err := writeSSE(w, "response.failed", map[string]any{
				"response": map[string]any{
					"id": respIDPrefix, "object": "response", "status": "failed", "model": model,
					"error": map[string]any{"code": "upstream_error", "message": msg},
				},
			}); err != nil {
				return err
			}
			flush()
			return nil
		}
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

var _ = strings.TrimSpace
