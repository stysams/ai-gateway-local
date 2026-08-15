// Package chat parses and rewrites OpenAI Chat Completions requests
// (docs/v1-scheme.md task package C). Only model and stream are inspected
// and rewritten; every other field is preserved with its exact raw value
// (json.RawMessage) so same-protocol forwarding is lossless at the field
// level. The document's key order and whitespace are not preserved: Rewrite
// re-marshals the field map, so byte-for-byte identity of the whole body is
// not guaranteed.
package chat

import (
	"encoding/json"
	"fmt"
)

// Request is the minimal view of a chat/completions request. Fields keeps
// the raw bytes of every top-level field, including unknown ones.
type Request struct {
	// Model is the requested model name; it is replaced by the routing
	// resolution before the request goes upstream.
	Model string
	// Stream is the client's streaming flag; nil means absent (false).
	Stream *bool
	// Fields carries every top-level field's raw JSON, so Rewrite can
	// produce a complete document without dropping unknown fields.
	Fields map[string]json.RawMessage
}

// Parse validates a chat/completions body and extracts model and stream.
// Unknown fields are preserved untouched; a body that is not a single JSON
// object is rejected.
func Parse(body []byte) (*Request, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	if fields == nil {
		return nil, fmt.Errorf("invalid JSON body: expected an object")
	}
	req := &Request{Fields: fields}
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

// StreamValue returns the effective streaming flag (absent means false).
func (r *Request) StreamValue() bool {
	if r.Stream != nil {
		return *r.Stream
	}
	return false
}

// Rewrite replaces the model and stream fields and returns the complete
// JSON document. All other fields (known and unknown) keep their exact raw
// values; only the document's key order and whitespace may change because
// the field map is re-marshalled.
func (r *Request) Rewrite(model string, stream bool) ([]byte, error) {
	m, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("marshal model: %w", err)
	}
	r.Fields["model"] = m
	s, err := json.Marshal(stream)
	if err != nil {
		return nil, fmt.Errorf("marshal stream: %w", err)
	}
	r.Fields["stream"] = s
	out, err := json.Marshal(r.Fields)
	if err != nil {
		return nil, fmt.Errorf("marshal rewritten body: %w", err)
	}
	return out, nil
}
