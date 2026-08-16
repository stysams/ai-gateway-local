package ir

import (
	"encoding/json"
	"testing"
)

func TestWrapUnwrapFreeformInput(t *testing.T) {
	wrapped := WrapFreeformInput("await tools.exec_command({cmd: 'hi'})")
	if got := UnwrapFreeformInput(wrapped); got != "await tools.exec_command({cmd: 'hi'})" {
		t.Fatalf("unwrap = %q", got)
	}
	if got := UnwrapFreeformInput(json.RawMessage(`"plain"`)); got != "plain" {
		t.Fatalf("string unwrap = %q", got)
	}
	if got := UnwrapFreeformInput(json.RawMessage(`{"city":"Berlin"}`)); got != `{"city":"Berlin"}` {
		t.Fatalf("object unwrap = %q", got)
	}
}

func TestSchemaFallbacks(t *testing.T) {
	if !ValidJSONObject(SchemaOrEmpty(nil)) {
		t.Fatal("empty schema is not an object")
	}
	if !ValidJSONObject(SchemaOrFreeform(nil)) {
		t.Fatal("freeform schema is not an object")
	}
	raw := json.RawMessage(`{"type":"object"}`)
	if string(SchemaOrEmpty(raw)) != string(raw) {
		t.Fatal("non-empty schema was replaced")
	}
}

func TestMarkCustomToolEvent(t *testing.T) {
	names := map[string]bool{"exec": true}
	ids := map[string]bool{}
	started := MarkCustomToolEvent(Event{Type: EventToolCallStarted, ToolCallID: "c1", ToolName: "exec"}, names, ids)
	if !started.ToolCustom || !ids["c1"] {
		t.Fatalf("started = %+v ids=%v", started, ids)
	}
	delta := MarkCustomToolEvent(Event{Type: EventToolCallArgumentsDlt, ToolCallID: "c1"}, names, ids)
	if !delta.ToolCustom {
		t.Fatal("delta not marked custom")
	}
	other := MarkCustomToolEvent(Event{Type: EventToolCallStarted, ToolCallID: "c2", ToolName: "wait"}, names, ids)
	if other.ToolCustom {
		t.Fatal("function tool marked custom")
	}
}
