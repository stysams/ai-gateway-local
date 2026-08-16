// Package ir is the protocol-independent intermediate representation
// (docs/v1-scheme.md §8). Inbound adapters parse client requests into ir
// types; outbound adapters generate upstream requests from them and parse
// upstream responses back into ir events. ir never imports a concrete
// protocol package, and adapters never call each other: cross-protocol
// conversion happens only through ir.
package ir

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrUnsupportedContent is returned by adapters when content cannot be
// represented without loss. Callers must answer 422 instead of silently
// dropping images, tools or other required content (docs/v1-scheme.md §8.4).
var ErrUnsupportedContent = errors.New("content cannot be converted without loss")

// ClientID identifies one of the four first-class clients. It mirrors
// route.ClientID without importing config-dependent packages.
type ClientID string

const (
	ClientCodex   ClientID = "codex"
	ClientClaude  ClientID = "claude"
	ClientGrok    ClientID = "grok"
	ClientGeneric ClientID = "generic"
)

// Protocol names the three wire protocols.
type Protocol string

const (
	ProtocolChat      Protocol = "chat"
	ProtocolResponses Protocol = "responses"
	ProtocolMessages  Protocol = "messages"
)

// Role is a message role in any protocol.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// BlockType is the kind of a content block.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockImage      BlockType = "image"
	BlockReasoning  BlockType = "reasoning"
	BlockToolCall   BlockType = "tool_call"
	BlockToolResult BlockType = "tool_result"
)

// Request is the protocol-independent request (docs/v1-scheme.md §8.1).
type Request struct {
	ID         string
	Client     ClientID
	Protocol   Protocol
	Model      string
	Stream     bool
	System     []Block
	Messages   []Message
	Tools      []Tool
	ToolChoice json.RawMessage
	Reasoning  ReasoningConfig
	// DroppedTools are hosted or otherwise non-convertible tool
	// definitions removed during inbound parse. The data plane logs them
	// as tool_dropped; they must not be silently discarded.
	DroppedTools []DroppedTool
	// Extensions carries fields the IR does not model. They are preserved
	// on same-protocol paths and dropped (with their names recorded) on
	// cross-protocol paths.
	Extensions map[string]json.RawMessage
}

// ReasoningConfig is the common request-level reasoning configuration.
// Chat and Responses share an effort setting. Messages uses a thinking mode
// and optional token budget; those fields stay separate because no reliable
// effort-to-budget conversion exists.
type ReasoningConfig struct {
	Enabled      bool
	Effort       string
	Summary      string
	Type         string
	BudgetTokens int64
	Display      string
	Source       Protocol
}

// RequestFeatures is the capability-relevant subset that can be inspected
// without forcing same-protocol requests through the cross-protocol parser.
type RequestFeatures struct {
	Image     bool
	Reasoning bool
}

// Empty reports whether the request contains no reasoning configuration.
func (r ReasoningConfig) Empty() bool {
	return !r.Enabled && r.Effort == "" && r.Summary == "" && r.Type == "" && r.BudgetTokens == 0 && r.Display == ""
}

// Message is one conversation turn.
type Message struct {
	Role    Role
	Content []Block
}

// Block is one content block of a message.
type Block struct {
	Type       BlockType
	Text       string
	Image      *Image
	Reasoning  *Reasoning
	ToolCall   *ToolCall
	ToolResult *ToolResult
}

// Image is a URL or base64 image (task package E).
type Image struct {
	URL       string
	Base64    string
	MediaType string
	Detail    string
}

// ParseImageURL converts a normal URL or an image data URL into the common
// image representation. Base64 is validated without retaining a decoded copy.
func ParseImageURL(value string) (*Image, error) {
	if !strings.HasPrefix(value, "data:") {
		if strings.TrimSpace(value) == "" {
			return nil, errors.New("image URL is empty")
		}
		return &Image{URL: value}, nil
	}
	header, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !ok || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return nil, errors.New("image data URL must use base64 encoding")
	}
	mediaType := strings.TrimSpace(strings.TrimSuffix(header, ";base64"))
	if !strings.HasPrefix(strings.ToLower(mediaType), "image/") || data == "" {
		return nil, errors.New("image data URL has an invalid media type or empty data")
	}
	if _, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding, strings.NewReader(data))); err != nil {
		return nil, fmt.Errorf("invalid image base64 data: %w", err)
	}
	return &Image{Base64: data, MediaType: mediaType}, nil
}

// WireURL returns the URL form accepted by both OpenAI protocols.
func (i *Image) WireURL() (string, error) {
	if i == nil {
		return "", errors.New("image is nil")
	}
	if i.URL != "" && i.Base64 == "" {
		return i.URL, nil
	}
	if i.Base64 != "" && strings.HasPrefix(strings.ToLower(i.MediaType), "image/") && i.URL == "" {
		return "data:" + i.MediaType + ";base64," + i.Base64, nil
	}
	return "", errors.New("image must contain exactly one URL or base64 source")
}

// Reasoning is a reasoning/thinking block (task package E).
type Reasoning struct {
	Text      string
	Signature string
	Encrypted string
}

// Tool is a function or custom/freeform tool definition.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	// Custom is true for Responses custom/freeform tools. JSON-only
	// protocols wrap the raw input in FreeformInputSchema.
	Custom bool
}

// DroppedTool records a tool definition that was removed or downgraded
// before the upstream request (docs/v1-scheme.md §8.4).
type DroppedTool struct {
	Type   string
	Name   string
	Reason string
}

// ToolCall is a model tool invocation with a stable id and complete JSON
// arguments (accumulated from deltas).
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
	Custom    bool
}

// ToolResult is the client's answer to a tool call.
type ToolResult struct {
	ID      string // the corresponding tool call id
	Content string
	IsError bool
}

// Usage aggregates token accounting across protocols.
type Usage struct {
	InputTokens     int64
	OutputTokens    int64
	ReasoningTokens int64
	TotalTokens     int64
}

// EventType is one of the unified response events (docs/v1-scheme.md §8.2).
type EventType string

const (
	EventStarted              EventType = "response.started"
	EventReasoningDelta       EventType = "reasoning.delta"
	EventReasoningCompleted   EventType = "reasoning.completed"
	EventTextDelta            EventType = "text.delta"
	EventTextCompleted        EventType = "text.completed"
	EventToolCallStarted      EventType = "tool_call.started"
	EventToolCallArgumentsDlt EventType = "tool_call.arguments.delta"
	EventToolCallCompleted    EventType = "tool_call.completed"
	EventUsage                EventType = "usage"
	EventCompleted            EventType = "response.completed"
	EventError                EventType = "error"
)

// ErrorInfo is a protocol-independent error carried by an error event.
type ErrorInfo struct {
	Type    string
	Code    string
	Message string
	// Status is the upstream HTTP status when the error came from an
	// upstream; 0 for local errors.
	Status int
}

// Event is one unified response event. Only the fields relevant to the
// event's Type are set.
type Event struct {
	Type EventType

	// Text events: Text carries the delta (EventTextDelta) or the complete
	// text (EventTextCompleted).
	Text string

	// Tool events: ToolCallID identifies the call; ToolName is set on
	// started/completed; ArgumentsDelta is one incremental fragment;
	// Arguments is the complete JSON on completed.
	ToolCallID     string
	ToolName       string
	ArgumentsDelta string
	Arguments      string
	// ToolCustom is set when the call targets a Responses custom tool.
	ToolCustom bool

	Usage Usage

	Error *ErrorInfo

	// StopReason is optional completion context (chat finish_reason,
	// messages stop_reason, responses status).
	StopReason string
}

// Sequencer validates and accumulates the event stream
// (docs/v1-scheme.md §8.2 constraints): tool call ids stay stable,
// argument deltas concatenate in arrival order, response.completed appears
// at most once, and no success event may follow an error event.
type Sequencer struct {
	started     bool
	completed   bool
	errored     bool
	activeTools map[string]*toolAccum
	toolOrder   []string
	reasoning   strings.Builder
	text        strings.Builder
	usage       Usage
	stopReason  string
}

type toolAccum struct {
	name      string
	arguments strings.Builder
	completed bool
}

// NewSequencer returns an empty sequencer.
func NewSequencer() *Sequencer {
	return &Sequencer{activeTools: map[string]*toolAccum{}}
}

// Push validates one event against the stream state and accumulates it.
func (s *Sequencer) Push(ev Event) error {
	switch ev.Type {
	case EventStarted:
		if s.started {
			return errors.New("event sequence: duplicate response.started")
		}
		if s.errored {
			return errors.New("event sequence: response.started after error")
		}
		s.started = true
		return nil

	case EventReasoningDelta, EventReasoningCompleted:
		if err := s.requireActive(ev.Type); err != nil {
			return err
		}
		if ev.Type == EventReasoningDelta && ev.Text != "" {
			s.reasoning.WriteString(ev.Text)
		}
		return nil

	case EventTextDelta, EventTextCompleted:
		if err := s.requireActive(ev.Type); err != nil {
			return err
		}
		if ev.Type == EventTextDelta {
			if ev.Text == "" {
				return nil // 空 delta 合法，不累积
			}
			s.text.WriteString(ev.Text)
		}
		return nil

	case EventToolCallStarted:
		if err := s.requireActive(ev.Type); err != nil {
			return err
		}
		if ev.ToolCallID == "" {
			return errors.New("event sequence: tool_call.started without id")
		}
		if _, dup := s.activeTools[ev.ToolCallID]; dup {
			return fmt.Errorf("event sequence: duplicate tool call id %q", ev.ToolCallID)
		}
		s.activeTools[ev.ToolCallID] = &toolAccum{name: ev.ToolName}
		s.toolOrder = append(s.toolOrder, ev.ToolCallID)
		return nil

	case EventToolCallArgumentsDlt:
		if err := s.requireActive(ev.Type); err != nil {
			return err
		}
		acc, ok := s.activeTools[ev.ToolCallID]
		if !ok {
			return fmt.Errorf("event sequence: arguments.delta for unknown tool call %q", ev.ToolCallID)
		}
		if acc.completed {
			return fmt.Errorf("event sequence: arguments.delta after completed for tool call %q", ev.ToolCallID)
		}
		acc.arguments.WriteString(ev.ArgumentsDelta)
		return nil

	case EventToolCallCompleted:
		if err := s.requireActive(ev.Type); err != nil {
			return err
		}
		acc, ok := s.activeTools[ev.ToolCallID]
		if !ok {
			return fmt.Errorf("event sequence: tool_call.completed for unknown tool call %q", ev.ToolCallID)
		}
		if acc.completed {
			return fmt.Errorf("event sequence: duplicate tool_call.completed for %q", ev.ToolCallID)
		}
		if ev.ToolName != "" {
			acc.name = ev.ToolName
		}
		acc.completed = true
		return nil

	case EventUsage:
		if err := s.requireActive(ev.Type); err != nil {
			return err
		}
		s.usage = ev.Usage
		return nil

	case EventCompleted:
		if !s.started {
			return errors.New("event sequence: response.completed before response.started")
		}
		if s.errored {
			return errors.New("event sequence: response.completed after error")
		}
		if s.completed {
			return errors.New("event sequence: duplicate response.completed")
		}
		s.completed = true
		s.stopReason = ev.StopReason
		return nil

	case EventError:
		if s.completed {
			return errors.New("event sequence: error after response.completed")
		}
		s.errored = true
		return nil

	default:
		return fmt.Errorf("event sequence: unknown event type %q", ev.Type)
	}
}

func (s *Sequencer) requireActive(typ EventType) error {
	if !s.started {
		return fmt.Errorf("event sequence: %s before response.started", typ)
	}
	if s.completed {
		return fmt.Errorf("event sequence: %s after response.completed", typ)
	}
	if s.errored {
		return fmt.Errorf("event sequence: %s after error", typ)
	}
	return nil
}

// Done reports whether a terminal state (completed or error) was reached.
func (s *Sequencer) Done() bool { return s.completed || s.errored }

// Errored reports whether an error event ended the stream.
func (s *Sequencer) Errored() bool { return s.errored }

// Completed reports whether response.completed was seen.
func (s *Sequencer) Completed() bool { return s.completed }

// Response is the aggregated outcome of an event stream.
type Response struct {
	Reasoning  string
	Text       string
	ToolCalls  []ToolCall
	Usage      Usage
	StopReason string
	Completed  bool
	Errored    bool
}

// Accumulate flattens the validated stream into a Response. Argument
// fragments are concatenated in arrival order per tool call; tool calls
// keep their stable order of first appearance.
func (s *Sequencer) Accumulate() *Response {
	resp := &Response{
		Reasoning:  s.reasoning.String(),
		Text:       s.text.String(),
		Usage:      s.usage,
		StopReason: s.stopReason,
		Completed:  s.completed,
		Errored:    s.errored,
	}
	for _, id := range s.toolOrder {
		acc := s.activeTools[id]
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID:        id,
			Name:      acc.name,
			Arguments: json.RawMessage(acc.arguments.String()),
		})
	}
	return resp
}
