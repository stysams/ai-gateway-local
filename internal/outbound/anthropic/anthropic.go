// Package anthropic is the outbound adapter for the Anthropic Messages API
// (docs/v1-scheme.md §10): <base_url>/v1/messages with x-api-key and a
// stable anthropic-version header. It generates requests from the
// ir.Request and parses non-streaming and SSE responses into ir events.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ai-gateway/internal/ir"
	"ai-gateway/internal/outbound/internal/upstream"
	"ai-gateway/internal/secret"
)

// ErrSecretMissing and ErrSecretStore are re-exported for callers.
var (
	ErrSecretMissing = upstream.ErrSecretMissing
	ErrSecretStore   = upstream.ErrSecretStore
)

// APIVersion is the stable Anthropic API version header value, matching the
// official SDK default (docs/v1-scheme.md §10).
const APIVersion = "2023-06-01"

// defaultMaxTokens is required by the Messages API; the IR does not model
// it at task package D.
const defaultMaxTokens = 4096

// Pool mirrors the other adapters' pool.
type Pool struct {
	secrets    secret.Store
	httpClient *http.Client
	transport  *http.Transport
}

// NewPool returns a pool over the shared transport.
func NewPool(secrets secret.Store) *Pool {
	tr := upstream.NewTransport()
	return &Pool{
		secrets:    secrets,
		httpClient: upstream.NoRedirectClient(tr),
		transport:  tr,
	}
}

// SetResponseHeaderTimeout adjusts the shared response-header timeout.
func (p *Pool) SetResponseHeaderTimeout(d time.Duration) {
	p.transport.ResponseHeaderTimeout = d
}

// Client is a stateless handle for one provider.
type Client struct {
	baseURL   string
	secretRef string
	secrets   secret.Store
	http      *http.Client
}

// Client returns a provider handle.
func (p *Pool) Client(baseURL, secretRef string) *Client {
	return &Client{
		baseURL:   baseURL,
		secretRef: secretRef,
		secrets:   p.secrets,
		http:      p.httpClient,
	}
}

// CompletionURL builds <base_url>/v1/messages without double slashes or a
// duplicated /v1 (docs/v1-scheme.md §10).
func CompletionURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

// Do sends a messages body upstream with the Anthropic auth headers.
func (c *Client) Do(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, CompletionURL(c.baseURL), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("User-Agent", "ai-gateway")
	req.Header.Set("anthropic-version", APIVersion)
	if c.secretRef != "" && c.secrets != nil {
		cred, err := upstream.XAPIKey(ctx, c.secrets, c.secretRef)
		if err != nil {
			return nil, err
		}
		defer cred.Zero()
		req.Header.Set(cred.Header, cred.Value)
	}
	return c.http.Do(req)
}

// GenerateRequest renders an ir.Request as a Messages request body.
func GenerateRequest(req *ir.Request) ([]byte, error) {
	body := map[string]any{
		"model":      req.Model,
		"stream":     req.Stream,
		"max_tokens": defaultMaxTokens,
	}
	var system strings.Builder
	for _, b := range req.System {
		switch b.Type {
		case ir.BlockText:
			if system.Len() > 0 {
				system.WriteString("\n\n")
			}
			system.WriteString(b.Text)
		case ir.BlockImage, ir.BlockReasoning:
			return nil, fmt.Errorf("%w: cannot convert %s system block to messages", ir.ErrUnsupportedContent, b.Type)
		}
	}
	if system.Len() > 0 {
		body["system"] = system.String()
	}

	var messages []map[string]any
	for _, m := range req.Messages {
		var content []map[string]any
		for _, b := range m.Content {
			switch b.Type {
			case ir.BlockText:
				content = append(content, map[string]any{"type": "text", "text": b.Text})
			case ir.BlockToolCall:
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    b.ToolCall.ID,
					"name":  b.ToolCall.Name,
					"input": json.RawMessage(b.ToolCall.Arguments),
				})
			case ir.BlockToolResult:
				content = append(content, map[string]any{
					"type":        "tool_result",
					"tool_use_id": b.ToolResult.ID,
					"content":     b.ToolResult.Content,
					"is_error":    b.ToolResult.IsError,
				})
			case ir.BlockImage:
				if b.Image == nil {
					return nil, fmt.Errorf("invalid image block: image is nil")
				}
				var source map[string]any
				switch {
				case b.Image.URL != "" && b.Image.Base64 == "":
					source = map[string]any{"type": "url", "url": b.Image.URL}
				case b.Image.Base64 != "" && b.Image.URL == "" && strings.HasPrefix(strings.ToLower(b.Image.MediaType), "image/"):
					source = map[string]any{"type": "base64", "media_type": b.Image.MediaType, "data": b.Image.Base64}
				default:
					return nil, fmt.Errorf("invalid image block: image must contain exactly one URL or base64 source")
				}
				content = append(content, map[string]any{"type": "image", "source": source})
			case ir.BlockReasoning:
				if b.Reasoning == nil {
					return nil, fmt.Errorf("invalid reasoning block: reasoning is nil")
				}
				if b.Reasoning.Encrypted != "" {
					content = append(content, map[string]any{"type": "redacted_thinking", "data": b.Reasoning.Encrypted})
				} else {
					block := map[string]any{"type": "thinking", "thinking": b.Reasoning.Text}
					if b.Reasoning.Signature != "" {
						block["signature"] = b.Reasoning.Signature
					}
					content = append(content, block)
				}
			}
		}
		role := string(m.Role)
		if role == string(ir.RoleTool) {
			role = string(ir.RoleUser) // tool_result 块位于 user 消息中
		}
		messages = append(messages, map[string]any{"role": role, "content": content})
	}
	body["messages"] = messages

	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": json.RawMessage(t.Parameters),
			})
		}
		body["tools"] = tools
	}
	if tc := toolChoiceForMessages(req.ToolChoice); tc != nil {
		body["tool_choice"] = tc
	}
	if !req.Reasoning.Empty() {
		if req.Reasoning.Source != ir.ProtocolMessages || req.Reasoning.Type == "" {
			return nil, fmt.Errorf("%w: reasoning configuration cannot be converted to messages thinking", ir.ErrUnsupportedContent)
		}
		thinking := map[string]any{"type": req.Reasoning.Type}
		if req.Reasoning.BudgetTokens > 0 {
			thinking["budget_tokens"] = req.Reasoning.BudgetTokens
		}
		if req.Reasoning.Display != "" {
			thinking["display"] = req.Reasoning.Display
		}
		body["thinking"] = thinking
	}
	return json.Marshal(body)
}

// toolChoiceForMessages maps the IR tool_choice onto the Messages form.
// The "required" mode has no Messages equivalent ("any" allows the model to
// pick any tool, which is not the same contract), so it is rejected by the
// caller via ErrToolChoice. This function returns nil for an unset choice.
func toolChoiceForMessages(tc json.RawMessage) any {
	if len(tc) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(tc, &s); err == nil {
		switch s {
		case "auto":
			return "auto"
		case "none":
			return "none"
		case "required":
			return "any"
		}
		return s
	}
	var named struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(tc, &named); err == nil && named.Type == "function" {
		return map[string]any{"type": "tool", "name": named.Name}
	}
	return nil
}

// ParseResponse converts a non-streaming Message into ir events.
func ParseResponse(body []byte) ([]ir.Event, error) {
	var resp struct {
		ID         string `json:"id"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Thinking  string          `json:"thinking"`
			Signature string          `json:"signature"`
			Data      string          `json:"data"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
		} `json:"content"`
		Usage *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode messages response: %w", err)
	}
	events := []ir.Event{{Type: ir.EventStarted, Text: resp.ID}}
	for _, block := range resp.Content {
		switch block.Type {
		case "thinking":
			if block.Thinking != "" {
				events = append(events,
					ir.Event{Type: ir.EventReasoningDelta, Text: block.Thinking},
					ir.Event{Type: ir.EventReasoningCompleted, Text: block.Thinking},
				)
			}
		case "redacted_thinking":
			// Redacted thinking is intentionally opaque and has no text event.
		case "text":
			if block.Text == "" {
				continue
			}
			events = append(events,
				ir.Event{Type: ir.EventTextDelta, Text: block.Text},
				ir.Event{Type: ir.EventTextCompleted, Text: block.Text},
			)
		case "tool_use":
			args := string(block.Input)
			events = append(events,
				ir.Event{Type: ir.EventToolCallStarted, ToolCallID: block.ID, ToolName: block.Name},
				ir.Event{Type: ir.EventToolCallArgumentsDlt, ToolCallID: block.ID, ArgumentsDelta: args},
				ir.Event{Type: ir.EventToolCallCompleted, ToolCallID: block.ID, ToolName: block.Name, Arguments: args},
			)
		}
	}
	if resp.Usage != nil {
		events = append(events, ir.Event{Type: ir.EventUsage, Usage: ir.Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			TotalTokens:  resp.Usage.InputTokens + resp.Usage.OutputTokens,
		}})
	}
	events = append(events, ir.Event{Type: ir.EventCompleted, StopReason: resp.StopReason})
	return events, nil
}

// streamLineMax caps a single SSE line.
const streamLineMax = 4 << 20

// StreamReader decodes the Messages SSE event stream into ir events.
type StreamReader struct {
	sc         *lineScanner
	callIDs    map[int64]string           // block index -> tool_use id
	args       map[int64]*strings.Builder // block index -> partial json
	reasoning  map[int64]bool
	stopReason string
}

// NewStreamReader wraps an upstream SSE body.
func NewStreamReader(r io.Reader) *StreamReader {
	return &StreamReader{
		sc:        newLineScanner(r, streamLineMax),
		callIDs:   map[int64]string{},
		args:      map[int64]*strings.Builder{},
		reasoning: map[int64]bool{},
	}
}

// Next returns the next ir event or io.EOF at stream end.
func (sr *StreamReader) Next() (ir.Event, error) {
	for {
		line, err := sr.sc.Next()
		if err != nil {
			return ir.Event{}, err
		}
		if !strings.HasPrefix(line, "event:") {
			continue
		}
		typ := strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		dataLine, err := sr.sc.Next()
		if err != nil {
			return ir.Event{}, fmt.Errorf("messages SSE: event %s without data: %w", typ, err)
		}
		data := strings.TrimSpace(strings.TrimPrefix(dataLine, "data:"))
		ev, err := sr.parse(typ, data)
		if err != nil {
			return ir.Event{}, err
		}
		if ev != nil {
			return *ev, nil
		}
	}
}

// parse converts one Messages SSE event into an ir event; nil means skip.
func (sr *StreamReader) parse(typ, data string) (*ir.Event, error) {
	switch typ {
	case "message_start":
		return &ir.Event{Type: ir.EventStarted}, nil
	case "content_block_start":
		var ev struct {
			Index int64 `json:"index"`
			Block struct {
				Type     string `json:"type"`
				ID       string `json:"id"`
				Name     string `json:"name"`
				Thinking string `json:"thinking"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("decode content_block_start: %w", err)
		}
		if ev.Block.Type == "tool_use" {
			sr.callIDs[ev.Index] = ev.Block.ID
			sr.args[ev.Index] = &strings.Builder{}
			return &ir.Event{Type: ir.EventToolCallStarted, ToolCallID: ev.Block.ID, ToolName: ev.Block.Name}, nil
		}
		if ev.Block.Type == "thinking" {
			sr.reasoning[ev.Index] = true
			if ev.Block.Thinking != "" {
				return &ir.Event{Type: ir.EventReasoningDelta, Text: ev.Block.Thinking}, nil
			}
		}
		return nil, nil // text block：文本由 delta 表达
	case "content_block_delta":
		var ev struct {
			Index int64 `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("decode content_block_delta: %w", err)
		}
		switch ev.Delta.Type {
		case "thinking_delta":
			return &ir.Event{Type: ir.EventReasoningDelta, Text: ev.Delta.Thinking}, nil
		case "text_delta":
			return &ir.Event{Type: ir.EventTextDelta, Text: ev.Delta.Text}, nil
		case "input_json_delta":
			callID, ok := sr.callIDs[ev.Index]
			if !ok {
				return nil, fmt.Errorf("messages SSE: input_json_delta for non-tool block %d", ev.Index)
			}
			if b := sr.args[ev.Index]; b != nil {
				b.WriteString(ev.Delta.PartialJSON)
			}
			return &ir.Event{Type: ir.EventToolCallArgumentsDlt, ToolCallID: callID, ArgumentsDelta: ev.Delta.PartialJSON}, nil
		}
		return nil, nil
	case "content_block_stop":
		var ev struct {
			Index int64 `json:"index"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("decode content_block_stop: %w", err)
		}
		if callID, ok := sr.callIDs[ev.Index]; ok {
			args := ""
			if b := sr.args[ev.Index]; b != nil {
				args = b.String()
			}
			return &ir.Event{Type: ir.EventToolCallCompleted, ToolCallID: callID, Arguments: args}, nil
		}
		if sr.reasoning[ev.Index] {
			delete(sr.reasoning, ev.Index)
			return &ir.Event{Type: ir.EventReasoningCompleted}, nil
		}
		return nil, nil
	case "message_delta":
		var ev struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
		}
		_ = json.Unmarshal([]byte(data), &ev)
		sr.stopReason = ev.Delta.StopReason
		return nil, nil
	case "message_stop":
		return &ir.Event{Type: ir.EventCompleted, StopReason: sr.stopReason}, nil
	case "error":
		var ev struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal([]byte(data), &ev)
		if ev.Error.Message == "" {
			// 兼容旧式扁平外形。
			var flat struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			}
			_ = json.Unmarshal([]byte(data), &flat)
			ev.Error.Type, ev.Error.Message = flat.Type, flat.Message
		}
		return &ir.Event{Type: ir.EventError, Error: &ir.ErrorInfo{Type: ev.Error.Type, Message: ev.Error.Message}}, nil
	default:
		// ping 等未识别事件：跳过。
		return nil, nil
	}
}

// lineScanner reads length-capped lines.
type lineScanner struct {
	r     io.Reader
	max   int
	buf   []byte
	chunk []byte
}

func newLineScanner(r io.Reader, max int) *lineScanner {
	return &lineScanner{r: r, max: max}
}

func (s *lineScanner) Next() (string, error) {
	s.buf = s.buf[:0]
	for {
		idx := indexByte(s.chunk, '\n')
		if idx >= 0 {
			line := append(s.buf, s.chunk[:idx]...)
			s.chunk = s.chunk[idx+1:]
			if len(line) > s.max {
				return "", fmt.Errorf("SSE line exceeds %d bytes", s.max)
			}
			return strings.TrimRight(string(line), "\r"), nil
		}
		s.buf = append(s.buf, s.chunk...)
		if len(s.buf) > s.max {
			return "", fmt.Errorf("SSE line exceeds %d bytes", s.max)
		}
		tmp := make([]byte, 32*1024)
		n, err := s.r.Read(tmp)
		if n > 0 {
			s.chunk = append(s.chunk[:0], tmp[:n]...)
			continue
		}
		if err != nil {
			if len(s.buf) == 0 {
				return "", err
			}
			line := s.buf
			s.buf = nil
			if len(line) > s.max {
				return "", fmt.Errorf("SSE line exceeds %d bytes", s.max)
			}
			return string(line), nil
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}
