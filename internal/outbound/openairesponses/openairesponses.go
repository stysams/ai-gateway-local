// Package openairesponses is the outbound adapter for the OpenAI Responses
// API (docs/v1-scheme.md §10): <base_url>/responses. It generates requests
// from the ir.Request and parses non-streaming and SSE responses into ir
// events, with the same auth and header discipline as the chat adapter.
package openairesponses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ai-gateway/internal/endpoint"
	"ai-gateway/internal/ir"
	"ai-gateway/internal/outbound/internal/upstream"
	"ai-gateway/internal/secret"
)

// ErrSecretMissing and ErrSecretStore are re-exported for callers.
var (
	ErrSecretMissing = upstream.ErrSecretMissing
	ErrSecretStore   = upstream.ErrSecretStore
)

// Pool mirrors openaichat.Pool: a shared transport with per-provider
// clients.
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
	endpoint  string
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

// CompletionURL builds the openai-responses completion URL. A base URL that
// does not already end with /v1 receives that prefix (docs/v1-scheme.md §10).
func CompletionURL(baseURL string) string {
	return endpoint.Join(baseURL, endpoint.Responses, "")
}

// WithEndpoint returns a client that posts to a user-maintained path.
func (c *Client) WithEndpoint(path string) *Client {
	next := *c
	next.endpoint = strings.TrimSpace(path)
	return &next
}

func (c *Client) requestURL() string {
	return endpoint.Join(c.baseURL, endpoint.Responses, c.endpoint)
}

func (c *Client) compactURL() string {
	return c.requestURL() + "/compact"
}

// CompactURL builds <base_url>/responses/compact for Codex remote compaction.
func CompactURL(baseURL string) string {
	return CompletionURL(baseURL) + "/compact"
}

// Do sends a responses body upstream. stream selects Accept.
func (c *Client) Do(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
	return c.DoWithHeaders(ctx, body, stream, nil)
}

// DoWithHeaders sends a Responses request with validated provider headers.
func (c *Client) DoWithHeaders(ctx context.Context, body []byte, stream bool, extraHeaders map[string]string) (*http.Response, error) {
	return c.do(ctx, c.requestURL(), body, stream, extraHeaders)
}

// DoCompact posts a unary compact request to /responses/compact.
func (c *Client) DoCompact(ctx context.Context, body []byte) (*http.Response, error) {
	return c.DoCompactWithHeaders(ctx, body, nil)
}

// DoCompactWithHeaders posts a compact request with provider headers.
func (c *Client) DoCompactWithHeaders(ctx context.Context, body []byte, extraHeaders map[string]string) (*http.Response, error) {
	return c.do(ctx, c.compactURL(), body, false, extraHeaders)
}

func (c *Client) do(ctx context.Context, url string, body []byte, stream bool, extraHeaders map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
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
	upstream.ApplyExtraHeaders(req.Header, extraHeaders)
	if c.secretRef != "" && c.secrets != nil {
		cred, err := upstream.Bearer(ctx, c.secrets, c.secretRef)
		if err != nil {
			return nil, err
		}
		defer cred.Zero()
		req.Header.Set(cred.Header, cred.Value)
	}
	return c.http.Do(req)
}

// GenerateRequest renders an ir.Request as a Responses request body
// (input items in the protocol's flat item list, tools, tool_choice).
func GenerateRequest(req *ir.Request) ([]byte, error) {
	body := map[string]any{
		"model":  req.Model,
		"stream": req.Stream,
	}
	if req.ToolChoice != nil {
		body["tool_choice"] = json.RawMessage(req.ToolChoice)
	}
	if req.Output != nil {
		format := map[string]any{
			"type": "json_schema", "name": req.Output.SchemaName(),
			"schema": json.RawMessage(req.Output.Schema), "strict": req.Output.Strict,
		}
		if req.Output.Description != "" {
			format["description"] = req.Output.Description
		}
		body["text"] = map[string]any{"format": format}
	}
	if !req.Reasoning.Empty() {
		if req.Reasoning.Source != ir.ProtocolChat && req.Reasoning.Source != ir.ProtocolResponses {
			return nil, fmt.Errorf("%w: thinking configuration cannot be converted to responses reasoning", ir.ErrUnsupportedContent)
		}
		reasoning := map[string]any{}
		if req.Reasoning.Effort != "" {
			reasoning["effort"] = req.Reasoning.Effort
		}
		if req.Reasoning.Summary != "" {
			reasoning["summary"] = req.Reasoning.Summary
		}
		body["reasoning"] = reasoning
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
			return nil, fmt.Errorf("%w: cannot convert %s system block to responses", ir.ErrUnsupportedContent, b.Type)
		}
	}
	if system.Len() > 0 {
		body["instructions"] = system.String()
	}

	var input []map[string]any
	for _, m := range req.Messages {
		switch m.Role {
		case ir.RoleTool:
			for _, b := range m.Content {
				if b.Type != ir.BlockToolResult {
					return nil, fmt.Errorf("cannot convert %s block to responses", b.Type)
				}
				input = append(input, functionCallOutputItem(b.ToolResult))
			}
		default:
			// Claude Code keeps tool_result on a user turn, sometimes next
			// to later user text. Dropping that block leaves a bare
			// function_call and the upstream returns 400
			// (docs/v1-scheme.md §10, §20 2026-08-16).
			var content []map[string]any
			flushContent := func() {
				if len(content) == 0 {
					return
				}
				input = append(input, map[string]any{
					"type":    "message",
					"role":    string(m.Role),
					"content": content,
				})
				content = nil
			}
			for _, b := range m.Content {
				switch b.Type {
				case ir.BlockText:
					// Responses only accepts output_text/refusal on
					// assistant items. User and developer stay input_text
					// (docs/v1-scheme.md §10, §20 2026-08-16).
					textType := "input_text"
					if m.Role == ir.RoleAssistant {
						textType = "output_text"
					}
					content = append(content, map[string]any{"type": textType, "text": b.Text})
				case ir.BlockToolCall:
					flushContent()
					if b.ToolCall.Custom {
						input = append(input, map[string]any{
							"type":    "custom_tool_call",
							"call_id": b.ToolCall.ID,
							"name":    b.ToolCall.Name,
							"input":   ir.UnwrapFreeformInput(b.ToolCall.Arguments),
						})
						break
					}
					input = append(input, map[string]any{
						"type":      "function_call",
						"call_id":   b.ToolCall.ID,
						"name":      b.ToolCall.Name,
						"arguments": string(b.ToolCall.Arguments),
					})
				case ir.BlockToolResult:
					flushContent()
					input = append(input, functionCallOutputItem(b.ToolResult))
				case ir.BlockImage:
					url, err := b.Image.WireURL()
					if err != nil {
						return nil, fmt.Errorf("invalid image block: %w", err)
					}
					part := map[string]any{"type": "input_image", "image_url": url}
					if b.Image.Detail != "" {
						part["detail"] = b.Image.Detail
					}
					content = append(content, part)
				case ir.BlockReasoning:
					return nil, fmt.Errorf("%w: reasoning history cannot be converted to a responses input item", ir.ErrUnsupportedContent)
				}
			}
			flushContent()
		}
	}
	body["input"] = input

	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			if t.Custom {
				tools = append(tools, map[string]any{
					"type":        "custom",
					"name":        t.Name,
					"description": t.Description,
				})
				continue
			}
			tool := map[string]any{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  json.RawMessage(t.Parameters),
			}
			if t.Strict {
				tool["strict"] = true
			}
			tools = append(tools, tool)
		}
		body["tools"] = tools
	}
	return json.Marshal(body)
}

func functionCallOutputItem(result *ir.ToolResult) map[string]any {
	item := map[string]any{
		"type":    "function_call_output",
		"call_id": "",
		"output":  "",
	}
	if result != nil {
		item["call_id"] = result.ID
		item["output"] = result.Content
	}
	return item
}

// ParseResponse converts a non-streaming Response into ir events.
func ParseResponse(body []byte) ([]ir.Event, error) {
	var resp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			CallID  string `json:"call_id"`
			Role    string `json:"role"`
			Name    string `json:"name"`
			Args    string `json:"arguments"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Summary []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"summary"`
		} `json:"output"`
		Usage *struct {
			InputTokens        int64 `json:"input_tokens"`
			OutputTokens       int64 `json:"output_tokens"`
			TotalTokens        int64 `json:"total_tokens"`
			InputTokensDetails *struct {
				CachedTokens *int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputTokensDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode responses body: %w", err)
	}
	events := []ir.Event{{Type: ir.EventStarted, Text: resp.ID}}
	for _, item := range resp.Output {
		switch item.Type {
		case "reasoning":
			var reasoning strings.Builder
			for _, part := range item.Summary {
				reasoning.WriteString(part.Text)
			}
			for _, part := range item.Content {
				if part.Type == "reasoning_text" {
					reasoning.WriteString(part.Text)
				}
			}
			if reasoning.Len() > 0 {
				events = append(events,
					ir.Event{Type: ir.EventReasoningDelta, Text: reasoning.String()},
					ir.Event{Type: ir.EventReasoningCompleted, Text: reasoning.String()},
				)
			}
		case "message":
			for _, part := range item.Content {
				if part.Type != "output_text" || part.Text == "" {
					continue
				}
				events = append(events,
					ir.Event{Type: ir.EventTextDelta, Text: part.Text},
					ir.Event{Type: ir.EventTextCompleted, Text: part.Text},
				)
			}
		case "function_call":
			callID := item.CallID
			if callID == "" {
				callID = item.ID
			}
			events = append(events,
				ir.Event{Type: ir.EventToolCallStarted, ToolCallID: callID, ToolName: item.Name},
				ir.Event{Type: ir.EventToolCallArgumentsDlt, ToolCallID: callID, ArgumentsDelta: item.Args},
				ir.Event{Type: ir.EventToolCallCompleted, ToolCallID: callID, ToolName: item.Name, Arguments: item.Args},
			)
		}
	}
	if resp.Usage != nil {
		cacheRead, cacheInput := int64(0), int64(0)
		if resp.Usage.InputTokensDetails != nil && resp.Usage.InputTokensDetails.CachedTokens != nil {
			cacheRead = *resp.Usage.InputTokensDetails.CachedTokens
			cacheInput = resp.Usage.InputTokens
		}
		events = append(events, ir.Event{Type: ir.EventUsage, Usage: ir.Usage{
			InputTokens:          resp.Usage.InputTokens,
			OutputTokens:         resp.Usage.OutputTokens,
			ReasoningTokens:      resp.Usage.OutputTokensDetails.ReasoningTokens,
			CacheReadInputTokens: cacheRead,
			CacheInputTokens:     cacheInput,
			TotalTokens:          resp.Usage.TotalTokens,
		}})
	}
	switch resp.Status {
	case "completed", "":
		events = append(events, ir.Event{Type: ir.EventCompleted, StopReason: resp.Status})
	case "failed", "incomplete":
		info := &ir.ErrorInfo{Type: "api_error", Code: "response_" + resp.Status,
			Message: fmt.Sprintf("upstream response status %q", resp.Status), Status: 200}
		if resp.Error != nil && resp.Error.Message != "" {
			info.Code = resp.Error.Code
			info.Message = resp.Error.Message
		}
		events = append(events, ir.Event{Type: ir.EventError, Error: info})
	default:
		events = append(events, ir.Event{Type: ir.EventCompleted, StopReason: resp.Status})
	}
	return events, nil
}

// streamLineMax caps a single SSE line.
const streamLineMax = 4 << 20

// StreamReader decodes the Responses SSE event stream into ir events. It
// tracks item ids to keep tool call ids stable between
// function_call_arguments.delta events and their item.
type StreamReader struct {
	sc      *lineScanner
	itemID  map[string]string // item_id -> call_id
	toolIdx map[string]string // item_id -> call_id (fallback by item)
	done    bool
}

// NewStreamReader wraps an upstream SSE body.
func NewStreamReader(r io.Reader) *StreamReader {
	return &StreamReader{
		sc:      newLineScanner(r, streamLineMax),
		itemID:  map[string]string{},
		toolIdx: map[string]string{},
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
			return ir.Event{}, fmt.Errorf("responses SSE: event %s without data: %w", typ, err)
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

// parse converts one Responses SSE event into an ir event; nil means the
// event type carries nothing for the IR (skip).
func (sr *StreamReader) parse(typ, data string) (*ir.Event, error) {
	switch typ {
	case "response.created", "response.in_progress":
		return &ir.Event{Type: ir.EventStarted}, nil
	case "response.output_item.added":
		var ev struct {
			Item struct {
				Type   string `json:"type"`
				ID     string `json:"id"`
				CallID string `json:"call_id"`
				Name   string `json:"name"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("decode output_item.added: %w", err)
		}
		if ev.Item.Type == "function_call" && ev.Item.CallID != "" {
			sr.itemID[ev.Item.ID] = ev.Item.CallID
			return &ir.Event{Type: ir.EventToolCallStarted, ToolCallID: ev.Item.CallID, ToolName: ev.Item.Name}, nil
		}
		return nil, nil
	case "response.output_text.delta":
		var ev struct {
			ItemID string `json:"item_id"`
			Delta  string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("decode output_text.delta: %w", err)
		}
		return &ir.Event{Type: ir.EventTextDelta, Text: ev.Delta}, nil
	case "response.reasoning_summary_text.delta", "response.reasoning_summary.delta", "response.reasoning_text.delta":
		var ev struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("decode %s: %w", typ, err)
		}
		return &ir.Event{Type: ir.EventReasoningDelta, Text: ev.Delta}, nil
	case "response.reasoning_summary_text.done", "response.reasoning_text.done":
		var ev struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("decode %s: %w", typ, err)
		}
		return &ir.Event{Type: ir.EventReasoningCompleted, Text: ev.Text}, nil
	case "response.function_call_arguments.delta":
		var ev struct {
			ItemID string `json:"item_id"`
			Delta  string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("decode function_call_arguments.delta: %w", err)
		}
		callID := sr.itemID[ev.ItemID]
		if callID == "" {
			return nil, fmt.Errorf("responses SSE: arguments delta for unknown item %q", ev.ItemID)
		}
		return &ir.Event{Type: ir.EventToolCallArgumentsDlt, ToolCallID: callID, ArgumentsDelta: ev.Delta}, nil
	case "response.function_call_arguments.done", "response.output_item.done":
		var ev struct {
			Item struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("decode %s: %w", typ, err)
		}
		if ev.Item.Type == "function_call" {
			callID := ev.Item.CallID
			if callID == "" {
				callID = sr.itemID[ev.Item.ID]
			}
			if callID == "" {
				return nil, fmt.Errorf("responses SSE: %s for unknown item %q", typ, ev.Item.ID)
			}
			return &ir.Event{Type: ir.EventToolCallCompleted, ToolCallID: callID, ToolName: ev.Item.Name, Arguments: ev.Item.Arguments}, nil
		}
		// arguments.done 的 data 是扁平结构（item_id/arguments，无 item
		// 嵌套）；output_item.done 才有 item。
		var flat struct {
			ItemID    string `json:"item_id"`
			Arguments string `json:"arguments"`
		}
		_ = json.Unmarshal([]byte(data), &flat)
		if flat.ItemID != "" && flat.Arguments != "" {
			callID := sr.itemID[flat.ItemID]
			if callID == "" {
				return nil, nil
			}
			return &ir.Event{Type: ir.EventToolCallCompleted, ToolCallID: callID, Arguments: flat.Arguments}, nil
		}
		return nil, nil
	case "response.output_text.done":
		var ev struct {
			Item struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"item"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("decode output_text.done: %w", err)
		}
		return &ir.Event{Type: ir.EventTextCompleted, Text: ev.Text}, nil
	case "response.completed":
		sr.done = true
		return &ir.Event{Type: ir.EventCompleted, StopReason: "completed"}, nil
	case "response.failed":
		var ev struct {
			Response struct {
				Error *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
		}
		_ = json.Unmarshal([]byte(data), &ev)
		info := &ir.ErrorInfo{Type: "api_error", Message: "upstream response failed"}
		if ev.Response.Error != nil {
			info.Code = ev.Response.Error.Code
			info.Message = ev.Response.Error.Message
		}
		return &ir.Event{Type: ir.EventError, Error: info}, nil
	case "error":
		var ev struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(data), &ev)
		return &ir.Event{Type: ir.EventError, Error: &ir.ErrorInfo{Type: "api_error", Code: ev.Code, Message: ev.Message}}, nil
	default:
		// Auxiliary or future events with no IR meaning are skipped. They must
		// never masquerade as a second response.started event.
		return nil, nil
	}
}

// lineScanner reads length-capped lines.
type lineScanner struct {
	r     io.Reader
	max   int
	buf   []byte
	chunk []byte
	tmp   []byte
}

func newLineScanner(r io.Reader, max int) *lineScanner {
	return &lineScanner{r: r, max: max, tmp: make([]byte, 32*1024)}
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
		n, err := s.r.Read(s.tmp)
		if n > 0 {
			s.chunk = append(s.chunk[:0], s.tmp[:n]...)
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
