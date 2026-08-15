package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"ai-gateway/internal/ir"
)

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

// idSuffix is the shared fake id base for synthesized responses.
const idSuffix = "chatcmpl-gateway"

// EncodeNonStream renders an aggregated ir.Response as a non-streaming
// chat.completion document.
func EncodeNonStream(w io.Writer, model string, resp *ir.Response) error {
	msg := map[string]any{
		"role":    "assistant",
		"content": resp.Text,
	}
	if resp.Reasoning != "" {
		msg["reasoning_content"] = resp.Reasoning
	}
	if len(resp.ToolCalls) > 0 {
		toolCalls := make([]map[string]any, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			toolCalls = append(toolCalls, map[string]any{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": string(tc.Arguments),
				},
			})
		}
		msg["tool_calls"] = toolCalls
	}
	out := map[string]any{
		"id":      idSuffix,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       msg,
			"finish_reason": finishReason(resp.StopReason),
		}},
	}
	if resp.Usage.TotalTokens > 0 || resp.Usage.InputTokens > 0 {
		out["usage"] = map[string]any{
			"prompt_tokens":     resp.Usage.InputTokens,
			"completion_tokens": resp.Usage.OutputTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("encode chat completion: %w", err)
	}
	_, err = w.Write(data)
	return err
}

// finishReason maps IR stop reasons onto the chat protocol's set.
func finishReason(stop string) string {
	switch stop {
	case "", "end_turn", "stop":
		return "stop"
	case "tool_use", "tool_calls":
		return "tool_calls"
	case "max_tokens", "length":
		return "length"
	case "completed":
		return "stop"
	}
	return "stop"
}

// EncodeStream renders ir events as a chat.completion.chunk SSE stream.
// Every event is flushed via flush immediately after writing. The stream
// ends with the data: [DONE] line after response.completed, or with an
// error event when the upstream failed.
func EncodeStream(w io.Writer, flush func(), model string, next func() (ir.Event, error)) error {
	toolIndex := map[string]int{}
	nextToolIndex := 0
	started := false
	finished := false
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
			started = true
			if err := writeChunk(w, model, chunkPayload{
				Delta: map[string]any{"role": "assistant", "content": ""},
			}); err != nil {
				return err
			}
			flush()
		case ir.EventTextDelta:
			if !started {
				started = true
			}
			if err := writeChunk(w, model, chunkPayload{
				Delta: map[string]any{"content": ev.Text},
			}); err != nil {
				return err
			}
			flush()
		case ir.EventReasoningDelta:
			if !started {
				started = true
			}
			if err := writeChunk(w, model, chunkPayload{
				Delta: map[string]any{"reasoning_content": ev.Text},
			}); err != nil {
				return err
			}
			flush()
		case ir.EventToolCallStarted:
			idx := nextToolIndex
			nextToolIndex++
			toolIndex[ev.ToolCallID] = idx
			if err := writeChunk(w, model, chunkPayload{
				Delta: map[string]any{"tool_calls": []any{map[string]any{
					"index": idx,
					"id":    ev.ToolCallID,
					"type":  "function",
					"function": map[string]any{
						"name":      ev.ToolName,
						"arguments": "",
					},
				}}},
			}); err != nil {
				return err
			}
			flush()
		case ir.EventToolCallArgumentsDlt:
			idx, ok := toolIndex[ev.ToolCallID]
			if !ok {
				continue
			}
			if err := writeChunk(w, model, chunkPayload{
				Delta: map[string]any{"tool_calls": []any{map[string]any{
					"index": idx,
					"id":    ev.ToolCallID,
					"function": map[string]any{
						"arguments": ev.ArgumentsDelta,
					},
				}}},
			}); err != nil {
				return err
			}
			flush()
		case ir.EventToolCallCompleted, ir.EventTextCompleted, ir.EventReasoningCompleted, ir.EventUsage:
			// chat 流式协议没有这些事件的直接对应：跳过。
		case ir.EventCompleted:
			finished = true
			if err := writeChunk(w, model, chunkPayload{
				Delta:        map[string]any{},
				FinishReason: finishReason(ev.StopReason),
			}); err != nil {
				return err
			}
			flush()
			_, err := io.WriteString(w, "data: [DONE]\n\n")
			if err != nil {
				return err
			}
			flush()
			return nil
		case ir.EventError:
			// 流中错误：以 OpenAI 错误事件结束，绝不伪造成功完成。
			msg := "upstream error"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			payload, _ := json.Marshal(map[string]any{"error": map[string]any{
				"message": msg,
				"type":    "upstream_error",
			}})
			if _, err := io.WriteString(w, "data: "+string(payload)+"\n\n"); err != nil {
				return err
			}
			flush()
			return nil
		}
	}
	// 上游断流（EOF 无 completed）：不得伪装成功完成。
	if !finished {
		payload, _ := json.Marshal(map[string]any{"error": map[string]any{
			"message": "upstream stream ended unexpectedly",
			"type":    "upstream_error",
		}})
		if _, err := io.WriteString(w, "data: "+string(payload)+"\n\n"); err != nil {
			return err
		}
		flush()
	}
	return nil
}

type chunkPayload struct {
	Delta        map[string]any `json:"delta"`
	FinishReason any            `json:"finish_reason"`
}

func writeChunk(w io.Writer, model string, cp chunkPayload) error {
	chunk := map[string]any{
		"id":      idSuffix,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         cp.Delta,
			"finish_reason": cp.FinishReason,
		}},
	}
	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "data: "+string(data)+"\n\n")
	return err
}
