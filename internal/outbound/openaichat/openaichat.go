// Package openaichat is the outbound adapter for OpenAI-compatible
// /chat/completions upstreams (docs/v1-scheme.md §10). It builds the exact
// upstream URL, generates requests from the ir.Request, parses non-streaming
// and SSE responses into ir events, injects the provider secret as
// Authorization: Bearer when a secret_ref is configured, and never forwards
// inbound client headers.
package openaichat

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

// ErrSecretMissing is re-exported for callers (server error mapping).
var ErrSecretMissing = upstream.ErrSecretMissing

// ErrSecretStore is re-exported for callers (server error mapping).
var ErrSecretStore = upstream.ErrSecretStore

// DefaultResponseHeaderTimeout is the production response-header timeout.
const DefaultResponseHeaderTimeout = upstream.DefaultResponseHeaderTimeout

// Pool owns one shared, safely configured transport (connection pool) and
// hands out per-provider clients that reuse it. Concurrent requests to
// different providers therefore share idle connections without sharing any
// per-request state (headers, auth or bodies).
type Pool struct {
	secrets    secret.Store
	httpClient *http.Client
	transport  *http.Transport
}

// NewPool returns a pool using the shared transport and the given key
// store. secrets may be nil; clients then never send Authorization.
func NewPool(secrets secret.Store) *Pool {
	tr := upstream.NewTransport()
	return &Pool{
		secrets:    secrets,
		httpClient: upstream.NoRedirectClient(tr),
		transport:  tr,
	}
}

// SetResponseHeaderTimeout adjusts the shared transport's response-header
// timeout. Used by tests and operators; the streaming body is never
// bounded.
func (p *Pool) SetResponseHeaderTimeout(d time.Duration) {
	p.transport.ResponseHeaderTimeout = d
}

// Client is a lightweight, stateless handle for one provider configuration.
// All clients of a Pool share the same connection pool.
type Client struct {
	baseURL   string
	secretRef string
	secrets   secret.Store
	http      *http.Client
}

// Client returns a handle for a provider with the given base URL and
// optional secret_ref. The handle is safe for concurrent use.
func (p *Pool) Client(baseURL, secretRef string) *Client {
	return &Client{
		baseURL:   baseURL,
		secretRef: secretRef,
		secrets:   p.secrets,
		http:      p.httpClient,
	}
}

// CompletionURL builds the exact upstream endpoint for the openai-chat
// adapter: <base_url>/chat/completions (docs/v1-scheme.md §10). A trailing
// slash is trimmed so no double slash can appear, and a base_url that
// already ends in /v1 (the presets' shape) naturally yields
// /v1/chat/completions without a duplicated /v1.
func CompletionURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/chat/completions"
}

// Do sends a chat/completions body upstream and returns the upstream
// response unmodified. stream selects the Accept header (text/event-stream
// for streaming, application/json otherwise). Only adapter-required headers
// are sent; inbound client headers never reach the upstream. The returned
// response's Body is owned by the caller. Errors never carry key material.
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
	if c.secretRef != "" && c.secrets != nil {
		cred, err := upstream.Bearer(ctx, c.secrets, c.secretRef)
		if err != nil {
			return nil, err
		}
		defer cred.Zero()
		req.Header.Set(cred.Header, cred.Value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		// Chat Completions requires arguments as a JSON string
		// whose contents are the raw argument JSON
		// (docs/v1-scheme.md §8.1, §10, §20 2026-08-16).
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
}

// GenerateRequest renders an ir.Request as a chat/completions request body.
// Extensions (unknown fields) are preserved when they are top-level fields
// the IR does not model, mirroring the same-protocol passthrough behavior.
//
// An assistant message with tool_calls must be followed immediately by a
// tool message for each tool_call_id (docs/v1-scheme.md §10, §20 2026-08-16).
// Responses history often places reasoning or later assistant text between
// the call and the result; those items are skipped or deferred so the pair
// stays adjacent.
func GenerateRequest(req *ir.Request) ([]byte, error) {
	body := map[string]any{
		"model":  req.Model,
		"stream": req.Stream,
	}
	if req.ToolChoice != nil {
		body["tool_choice"] = json.RawMessage(req.ToolChoice)
	}
	if !req.Reasoning.Empty() {
		if req.Reasoning.Effort == "" || (req.Reasoning.Source != ir.ProtocolChat && req.Reasoning.Source != ir.ProtocolResponses) {
			return nil, fmt.Errorf("%w: reasoning configuration cannot be converted to chat/completions", ir.ErrUnsupportedContent)
		}
		body["reasoning_effort"] = req.Reasoning.Effort
	}
	var messages []chatMessage
	if len(req.System) > 0 {
		var text string
		for _, b := range req.System {
			if b.Type != ir.BlockText {
				return nil, fmt.Errorf("%w: cannot convert %s system block to chat/completions", ir.ErrUnsupportedContent, b.Type)
			}
			if text != "" {
				text += "\n\n"
			}
			text += b.Text
		}
		messages = append(messages, chatMessage{Role: string(ir.RoleSystem), Content: text})
	}
	usedResults := map[string]bool{}
	for i, m := range req.Messages {
		if resultIDs := irToolResultIDs(m); len(resultIDs) > 0 && allIDsUsed(resultIDs, usedResults) {
			continue
		}
		cm, ok, err := encodeChatMessage(m)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		messages = append(messages, cm)
		if len(cm.ToolCalls) == 0 {
			continue
		}
		needed := map[string]bool{}
		for _, tc := range cm.ToolCalls {
			if tc.ID != "" {
				needed[tc.ID] = true
			}
		}
		for j := i + 1; j < len(req.Messages) && len(needed) > 0; j++ {
			ids := irToolResultIDs(req.Messages[j])
			if !anyIDNeeded(ids, needed) {
				continue
			}
			tm, tok, err := encodeChatMessage(req.Messages[j])
			if err != nil {
				return nil, err
			}
			if !tok {
				continue
			}
			messages = append(messages, tm)
			for _, id := range ids {
				if needed[id] {
					usedResults[id] = true
					delete(needed, id)
				}
			}
		}
	}
	body["messages"] = messages

	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  json.RawMessage(t.Parameters),
				},
			})
		}
		body["tools"] = tools
	}
	for k, v := range req.Extensions {
		if _, declared := body[k]; !declared {
			body[k] = json.RawMessage(v)
		}
	}
	return json.Marshal(body)
}

func encodeChatMessage(m ir.Message) (chatMessage, bool, error) {
	cm := chatMessage{Role: string(m.Role)}
	var text string
	var parts []map[string]any
	hasImage := false
	var toolCalls []chatToolCall
	for _, b := range m.Content {
		switch b.Type {
		case ir.BlockText:
			parts = append(parts, map[string]any{"type": "text", "text": b.Text})
			if text != "" {
				text += "\n\n"
			}
			text += b.Text
		case ir.BlockToolCall:
			ctc := chatToolCall{ID: b.ToolCall.ID, Type: "function"}
			ctc.Function.Name = b.ToolCall.Name
			ctc.Function.Arguments = string(b.ToolCall.Arguments)
			toolCalls = append(toolCalls, ctc)
		case ir.BlockToolResult:
			cm.ToolCallID = b.ToolResult.ID
			text = b.ToolResult.Content
			if b.ToolResult.IsError {
				text = "Error: " + text
			}
		case ir.BlockImage:
			url, err := b.Image.WireURL()
			if err != nil {
				return chatMessage{}, false, fmt.Errorf("invalid image block: %w", err)
			}
			imageURL := map[string]any{"url": url}
			if b.Image.Detail != "" {
				imageURL["detail"] = b.Image.Detail
			}
			parts = append(parts, map[string]any{"type": "image_url", "image_url": imageURL})
			hasImage = true
		case ir.BlockReasoning:
			return chatMessage{}, false, fmt.Errorf("%w: reasoning content cannot be converted to chat/completions", ir.ErrUnsupportedContent)
		}
	}
	if hasImage {
		cm.Content = parts
	} else {
		cm.Content = text
	}
	if len(toolCalls) > 0 {
		cm.ToolCalls = toolCalls
	}
	if !hasImage && text == "" && len(toolCalls) == 0 && cm.ToolCallID == "" {
		return chatMessage{}, false, nil
	}
	return cm, true, nil
}

func irToolResultIDs(m ir.Message) []string {
	var ids []string
	for _, b := range m.Content {
		if b.Type == ir.BlockToolResult && b.ToolResult != nil && b.ToolResult.ID != "" {
			ids = append(ids, b.ToolResult.ID)
		}
	}
	return ids
}

func allIDsUsed(ids []string, used map[string]bool) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if !used[id] {
			return false
		}
	}
	return true
}

func anyIDNeeded(ids []string, needed map[string]bool) bool {
	for _, id := range ids {
		if needed[id] {
			return true
		}
	}
	return false
}

// ParseResponse converts a non-streaming chat.completion response into the
// unified event sequence. Unknown response fields are ignored; the events
// carry the protocol's semantic content only.
func ParseResponse(body []byte) ([]ir.Event, error) {
	var resp struct {
		ID      string `json:"id"`
		Choices []struct {
			Index   int64 `json:"index"`
			Message struct {
				Role             string `json:"role"`
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens            int64 `json:"prompt_tokens"`
			CompletionTokens        int64 `json:"completion_tokens"`
			TotalTokens             int64 `json:"total_tokens"`
			CompletionTokensDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	events := []ir.Event{{Type: ir.EventStarted, Text: resp.ID}}
	for _, ch := range resp.Choices {
		if reasoning := ch.Message.ReasoningContent; reasoning != "" {
			events = append(events,
				ir.Event{Type: ir.EventReasoningDelta, Text: reasoning},
				ir.Event{Type: ir.EventReasoningCompleted, Text: reasoning},
			)
		}
		if text := ch.Message.Content; text != "" {
			events = append(events,
				ir.Event{Type: ir.EventTextDelta, Text: text},
				ir.Event{Type: ir.EventTextCompleted, Text: text},
			)
		}
		for _, tc := range ch.Message.ToolCalls {
			// chat 的 arguments 是 JSON 字符串：解包为原始 JSON。
			args := tc.Function.Arguments
			events = append(events,
				ir.Event{Type: ir.EventToolCallStarted, ToolCallID: tc.ID, ToolName: tc.Function.Name},
				ir.Event{Type: ir.EventToolCallArgumentsDlt, ToolCallID: tc.ID, ArgumentsDelta: args},
				ir.Event{Type: ir.EventToolCallCompleted, ToolCallID: tc.ID, ToolName: tc.Function.Name, Arguments: args},
			)
		}
	}
	if resp.Usage != nil {
		events = append(events, ir.Event{Type: ir.EventUsage, Usage: ir.Usage{
			InputTokens:     resp.Usage.PromptTokens,
			OutputTokens:    resp.Usage.CompletionTokens,
			ReasoningTokens: resp.Usage.CompletionTokensDetails.ReasoningTokens,
			TotalTokens:     resp.Usage.TotalTokens,
		}})
	}
	stop := ""
	if len(resp.Choices) > 0 && resp.Choices[0].FinishReason != nil {
		stop = *resp.Choices[0].FinishReason
	}
	events = append(events, ir.Event{Type: ir.EventCompleted, StopReason: stop})
	return events, nil
}

// streamLineMax caps a single SSE line to protect memory.
const streamLineMax = 4 << 20

// StreamReader decodes a chat/completions SSE body into ir events. One
// chunk may expand into several events (text delta, tool fragments,
// finish); the reader flattens them through an internal queue so each Next
// returns exactly one event. It never buffers the whole stream.
//
// Subsequent tool-call chunks often omit id and only carry index, matching
// the official Chat Completions stream. IDs are recovered from the first
// fragment of each index.
type StreamReader struct {
	sc        *lineScanner
	pending   []ir.Event
	started   bool
	callIDs   map[int64]string
	callNames map[int64]string
	args      map[int64]*strings.Builder
	callOrder []int64
}

// NewStreamReader wraps an upstream SSE body.
func NewStreamReader(r io.Reader) *StreamReader {
	return &StreamReader{
		sc:        newLineScanner(r, streamLineMax),
		callIDs:   map[int64]string{},
		callNames: map[int64]string{},
		args:      map[int64]*strings.Builder{},
	}
}

// Next returns the next event, or io.EOF at the end of the stream (the
// terminal [DONE] line, or a clean EOF). A transport error is returned as
// an ir error event so callers can decide how to end the client response.
func (sr *StreamReader) Next() (ir.Event, error) {
	for {
		if len(sr.pending) > 0 {
			ev := sr.pending[0]
			sr.pending = sr.pending[1:]
			return ev, nil
		}
		line, err := sr.sc.Next()
		if err != nil {
			return ir.Event{}, err
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return ir.Event{}, io.EOF
		}
		events, err := sr.parseChunk(data)
		if err != nil {
			return ir.Event{}, err
		}
		sr.pending = append(sr.pending, events...)
	}
}

// parseChunk converts one chat.completion.chunk into ir events.
func (sr *StreamReader) parseChunk(data string) ([]ir.Event, error) {
	var chunk struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Choices []struct {
			Index int64 `json:"index"`
			Delta struct {
				Role             string          `json:"role"`
				Content          string          `json:"content"`
				ReasoningContent string          `json:"reasoning_content"`
				ToolCalls        []chatToolDelta `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil, fmt.Errorf("decode chat chunk: %w", err)
	}
	var events []ir.Event
	for _, ch := range chunk.Choices {
		if ch.Delta.Role != "" || ch.Delta.ReasoningContent != "" || ch.Delta.Content != "" || len(ch.Delta.ToolCalls) > 0 {
			events = sr.ensureStarted(events)
		}
		if ch.Delta.ReasoningContent != "" {
			events = append(events, ir.Event{Type: ir.EventReasoningDelta, Text: ch.Delta.ReasoningContent})
		}
		if ch.Delta.Content != "" {
			events = append(events, ir.Event{Type: ir.EventTextDelta, Text: ch.Delta.Content})
		}
		for _, tc := range ch.Delta.ToolCalls {
			events = append(events, sr.toolCallEvents(tc)...)
		}
		if ch.FinishReason != nil && *ch.FinishReason != "" {
			events = append(events, sr.finishToolCalls()...)
			events = append(events, ir.Event{Type: ir.EventCompleted, StopReason: *ch.FinishReason})
		}
	}
	return events, nil
}

func (sr *StreamReader) ensureStarted(events []ir.Event) []ir.Event {
	if sr.started {
		return events
	}
	sr.started = true
	return append(events, ir.Event{Type: ir.EventStarted})
}

type chatToolDelta struct {
	Index    int64  `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (sr *StreamReader) toolCallEvents(tc chatToolDelta) []ir.Event {
	var events []ir.Event
	id := tc.ID
	if id == "" {
		id = sr.callIDs[tc.Index]
	}
	if tc.ID != "" && sr.callIDs[tc.Index] == "" {
		sr.callIDs[tc.Index] = tc.ID
		sr.callOrder = append(sr.callOrder, tc.Index)
		if sr.args[tc.Index] == nil {
			sr.args[tc.Index] = &strings.Builder{}
		}
		events = append(events, ir.Event{Type: ir.EventToolCallStarted, ToolCallID: tc.ID, ToolName: tc.Function.Name})
	}
	if tc.Function.Name != "" {
		sr.callNames[tc.Index] = tc.Function.Name
	}
	if tc.Function.Arguments == "" {
		return events
	}
	if sr.args[tc.Index] == nil {
		sr.args[tc.Index] = &strings.Builder{}
	}
	sr.args[tc.Index].WriteString(tc.Function.Arguments)
	if id == "" {
		return events
	}
	events = append(events, ir.Event{
		Type: ir.EventToolCallArgumentsDlt, ToolCallID: id, ArgumentsDelta: tc.Function.Arguments,
	})
	return events
}

func (sr *StreamReader) finishToolCalls() []ir.Event {
	var events []ir.Event
	for _, idx := range sr.callOrder {
		id := sr.callIDs[idx]
		if id == "" {
			continue
		}
		args := ""
		if b := sr.args[idx]; b != nil {
			args = b.String()
		}
		events = append(events, ir.Event{
			Type:       ir.EventToolCallCompleted,
			ToolCallID: id,
			ToolName:   sr.callNames[idx],
			Arguments:  args,
		})
	}
	sr.callOrder = nil
	return events
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

// Next returns the next line without the trailing newline.
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
