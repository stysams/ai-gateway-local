package ir

import (
	"encoding/json"
	"strings"
	"testing"
)

func textDelta(s string) Event { return Event{Type: EventTextDelta, Text: s} }
func textDone(s string) Event  { return Event{Type: EventTextCompleted, Text: s} }

func TestSequencerTextOrder(t *testing.T) {
	s := NewSequencer()
	for _, ev := range []Event{
		{Type: EventStarted},
		textDelta("Hel"),
		textDelta("lo"),
		textDelta(" "),
		textDelta("world"),
		textDone("Hello world"),
		{Type: EventUsage, Usage: Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}},
		{Type: EventCompleted, StopReason: "stop"},
	} {
		if err := s.Push(ev); err != nil {
			t.Fatalf("Push(%s): %v", ev.Type, err)
		}
	}
	resp := s.Accumulate()
	if resp.Text != "Hello world" {
		t.Errorf("text = %q, want %q", resp.Text, "Hello world")
	}
	if !resp.Completed || resp.Errored {
		t.Errorf("completed=%v errored=%v", resp.Completed, resp.Errored)
	}
	if resp.Usage.TotalTokens != 5 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestSequencerReasoningOrder(t *testing.T) {
	s := NewSequencer()
	for _, ev := range []Event{
		{Type: EventStarted},
		{Type: EventReasoningDelta, Text: "step "},
		{Type: EventReasoningDelta, Text: "one"},
		{Type: EventReasoningCompleted, Text: "step one"},
		{Type: EventCompleted},
	} {
		if err := s.Push(ev); err != nil {
			t.Fatalf("Push(%s): %v", ev.Type, err)
		}
	}
	if got := s.Accumulate().Reasoning; got != "step one" {
		t.Fatalf("reasoning = %q", got)
	}
}

func TestImageURLRoundTrip(t *testing.T) {
	for _, value := range []string{
		"https://example.com/image.png",
		"data:image/png;base64,AAAA",
	} {
		image, err := ParseImageURL(value)
		if err != nil {
			t.Fatalf("ParseImageURL(%q): %v", value, err)
		}
		got, err := image.WireURL()
		if err != nil {
			t.Fatal(err)
		}
		if got != value {
			t.Fatalf("round trip = %q, want %q", got, value)
		}
	}
	if _, err := ParseImageURL("data:text/plain;base64,AAAA"); err == nil {
		t.Fatal("non-image data URL accepted")
	}
}

func TestSequencerArgumentDeltaConcatenation(t *testing.T) {
	s := NewSequencer()
	for _, ev := range []Event{
		{Type: EventStarted},
		{Type: EventToolCallStarted, ToolCallID: "call_1", ToolName: "get_weather"},
		{Type: EventToolCallArgumentsDlt, ToolCallID: "call_1", ArgumentsDelta: `{"city": "Ber`},
		{Type: EventToolCallArgumentsDlt, ToolCallID: "call_1", ArgumentsDelta: `lin","unit": "c"}`},
		{Type: EventToolCallCompleted, ToolCallID: "call_1", Arguments: `{"city": "Berlin","unit": "c"}`},
		{Type: EventCompleted, StopReason: "tool_calls"},
	} {
		if err := s.Push(ev); err != nil {
			t.Fatalf("Push: %v", err)
		}
	}
	resp := s.Accumulate()
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "get_weather" {
		t.Errorf("tool call = %+v", tc)
	}
	if got := string(tc.Arguments); got != `{"city": "Berlin","unit": "c"}` {
		t.Errorf("arguments = %q (deltas must concatenate in order)", got)
	}
}

func TestSequencerMultipleToolCallsDoNotInterleave(t *testing.T) {
	s := NewSequencer()
	for _, ev := range []Event{
		{Type: EventStarted},
		{Type: EventToolCallStarted, ToolCallID: "call_a", ToolName: "fn_a"},
		{Type: EventToolCallStarted, ToolCallID: "call_b", ToolName: "fn_b"},
		{Type: EventToolCallArgumentsDlt, ToolCallID: "call_b", ArgumentsDelta: `{"b": 1}`},
		{Type: EventToolCallArgumentsDlt, ToolCallID: "call_a", ArgumentsDelta: `{"a": 1}`},
		{Type: EventToolCallCompleted, ToolCallID: "call_a"},
		{Type: EventToolCallCompleted, ToolCallID: "call_b"},
		{Type: EventCompleted},
	} {
		if err := s.Push(ev); err != nil {
			t.Fatalf("Push: %v", err)
		}
	}
	resp := s.Accumulate()
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(resp.ToolCalls))
	}
	// 每个调用收到自己的增量，不串。
	if got := string(resp.ToolCalls[0].Arguments); got != `{"a": 1}` {
		t.Errorf("call_a arguments = %q", got)
	}
	if got := string(resp.ToolCalls[1].Arguments); got != `{"b": 1}` {
		t.Errorf("call_b arguments = %q", got)
	}
	// 保持首次出现顺序。
	if resp.ToolCalls[0].ID != "call_a" || resp.ToolCalls[1].ID != "call_b" {
		t.Errorf("order = %s, %s", resp.ToolCalls[0].ID, resp.ToolCalls[1].ID)
	}
}

func TestSequencerRejectsInvalidSequences(t *testing.T) {
	cases := []struct {
		name  string
		event Event
	}{
		{"delta before started", textDelta("x")},
		{"completed twice", Event{Type: EventCompleted}},
		{"completed before started", Event{Type: EventCompleted}},
		{"delta after completed", textDelta("x")},
		{"delta after error", textDelta("x")},
		{"started twice", Event{Type: EventStarted}},
		{"unknown tool delta", Event{Type: EventToolCallArgumentsDlt, ToolCallID: "ghost", ArgumentsDelta: "{}"}},
		{"tool started without id", Event{Type: EventToolCallStarted}},
		{"duplicate tool id", Event{Type: EventToolCallStarted, ToolCallID: "call_x"}},
	}
	// 每个用例都在一个全新且已 started 的序列上测试。
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSequencer()
			_ = s.Push(Event{Type: EventStarted})
			_ = s.Push(Event{Type: EventToolCallStarted, ToolCallID: "call_x", ToolName: "f"})
			switch tc.name {
			case "completed twice":
				_ = s.Push(Event{Type: EventCompleted})
			case "completed before started", "delta before started":
				s = NewSequencer() // 无 started
			case "delta after completed":
				_ = s.Push(Event{Type: EventCompleted})
			case "delta after error":
				_ = s.Push(Event{Type: EventError})
			case "started twice":
				_ = s.Push(Event{Type: EventStarted})
			case "unknown tool delta":
				s = NewSequencer()
				_ = s.Push(Event{Type: EventStarted})
			case "duplicate tool id":
				_ = s.Push(Event{Type: EventToolCallStarted, ToolCallID: "call_x", ToolName: "g"})
			}
			if err := s.Push(tc.event); err == nil {
				t.Errorf("Push accepted %s, want error", tc.name)
			}
		})
	}
}

func TestSequencerErrorThenNoCompletion(t *testing.T) {
	s := NewSequencer()
	_ = s.Push(Event{Type: EventStarted})
	_ = s.Push(textDelta("partial"))
	if err := s.Push(Event{Type: EventError, Error: &ErrorInfo{Type: "api_error", Message: "boom"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Push(Event{Type: EventCompleted}); err == nil {
		t.Fatal("completed after error accepted")
	}
	resp := s.Accumulate()
	if !resp.Errored || resp.Completed {
		t.Errorf("errored=%v completed=%v", resp.Errored, resp.Completed)
	}
	// 错误后仍有已到达的文本（非成功完成语义）。
	if resp.Text != "partial" {
		t.Errorf("text = %q", resp.Text)
	}
}

func TestSequencerEmptyDeltaIsSkipped(t *testing.T) {
	s := NewSequencer()
	_ = s.Push(Event{Type: EventStarted})
	if err := s.Push(textDelta("")); err != nil {
		t.Fatal(err)
	}
	if s.text.Len() != 0 {
		t.Error("empty delta accumulated")
	}
}

func TestBlockHelpers(t *testing.T) {
	req := Request{
		Model:    "m",
		Messages: []Message{{Role: RoleUser, Content: []Block{{Type: BlockText, Text: "hi"}}}},
		Tools:    []Tool{{Name: "f", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
	if req.Messages[0].Content[0].Text != "hi" {
		t.Error("block text lost")
	}
	if !strings.Contains(string(req.Tools[0].Parameters), "object") {
		t.Error("tool parameters lost")
	}
}
