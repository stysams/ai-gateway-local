package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"ai-gateway/internal/config"
	"ai-gateway/internal/inbound/chat"
	"ai-gateway/internal/inbound/messages"
	"ai-gateway/internal/inbound/responses"
	"ai-gateway/internal/ir"
	"ai-gateway/internal/outbound/anthropic"
	"ai-gateway/internal/outbound/openaichat"
	"ai-gateway/internal/outbound/openairesponses"
	"ai-gateway/internal/route"
)

// maxChatBody bounds data-plane request bodies (docs/v1-scheme.md §9.3).
const maxChatBody = 128 << 20

// chatAdapter is the adapter name for the Chat protocol.
const chatAdapter = "openai-chat"

// adapterProtocol maps a provider adapter onto its wire protocol. The
// config validation guarantees the closed set.
func adapterProtocol(adapter string) ir.Protocol {
	switch adapter {
	case "openai-chat":
		return ir.ProtocolChat
	case "openai-responses":
		return ir.ProtocolResponses
	case "anthropic":
		return ir.ProtocolMessages
	}
	return ""
}

// openAIError is the native error shape of the OpenAI protocols, used for
// data-plane errors (docs/v1-scheme.md §9.5).
type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

type openAIErrorBody struct {
	Error openAIError `json:"error"`
}

// writeOpenAIError answers with the OpenAI-native error envelope.
func writeOpenAIError(w http.ResponseWriter, status int, errType, message, code string) {
	writeJSON(w, status, openAIErrorBody{Error: openAIError{Message: message, Type: errType, Code: code}})
}

// writeInboundError answers with the inbound protocol's native error shape.
func writeInboundError(w http.ResponseWriter, status int, proto ir.Protocol, message, code string) {
	switch proto {
	case ir.ProtocolChat, ir.ProtocolResponses:
		writeOpenAIError(w, status, "invalid_request_error", message, code)
	case ir.ProtocolMessages:
		writeJSON(w, status, map[string]any{"type": "invalid_request_error", "message": message})
	}
}

// forwardHeaders is the whitelist of upstream response headers forwarded to
// the client. It deliberately excludes Set-Cookie, Authorization and all
// hop-by-hop headers (Connection, Keep-Alive, Transfer-Encoding, Upgrade,
// Proxy-*, TE, Trailer).
var forwardHeaders = []string{
	"Content-Type",
	"Cache-Control",
	"Retry-After",
	"X-Request-Id",
	"OpenAI-Request-ID",
	"X-RateLimit-Limit",
	"X-RateLimit-Remaining",
	"X-RateLimit-Reset",
	"Location", // redirects are forwarded, never followed (see Pool)
}

// isJSONContentType reports whether a request Content-Type is JSON per
// docs/v1-scheme.md §9.3: application/json or a +json media type.
func isJSONContentType(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	mt, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return false
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// ---- endpoints -------------------------------------------------------------

// handleChatCompletions serves POST /v1/chat/completions (no prefix is
// equivalent to /c/generic, docs/v1-scheme.md §7.2).
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.serveDataPlane(w, r, route.Generic, ir.ProtocolChat)
}

// handleChatCompletionsClient serves POST /c/{client}/v1/chat/completions.
func (s *Server) handleChatCompletionsClient(w http.ResponseWriter, r *http.Request) {
	s.serveClientDataPlane(w, r, ir.ProtocolChat)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	s.serveDataPlane(w, r, route.Generic, ir.ProtocolResponses)
}

func (s *Server) handleResponsesClient(w http.ResponseWriter, r *http.Request) {
	s.serveClientDataPlane(w, r, ir.ProtocolResponses)
}

func (s *Server) handleResponsesCompact(w http.ResponseWriter, r *http.Request) {
	s.serveDataPlaneWith(w, r, route.Generic, ir.ProtocolResponses, dataPlaneOptions{compact: true})
}

func (s *Server) handleResponsesCompactClient(w http.ResponseWriter, r *http.Request) {
	s.serveClientDataPlaneWith(w, r, ir.ProtocolResponses, dataPlaneOptions{compact: true})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	s.serveDataPlane(w, r, route.Generic, ir.ProtocolMessages)
}

func (s *Server) handleMessagesClient(w http.ResponseWriter, r *http.Request) {
	s.serveClientDataPlane(w, r, ir.ProtocolMessages)
}

type dataPlaneOptions struct {
	compact bool
}

// serveClientDataPlane validates the client path segment (404 on unknown).
func (s *Server) serveClientDataPlane(w http.ResponseWriter, r *http.Request, proto ir.Protocol) {
	s.serveClientDataPlaneWith(w, r, proto, dataPlaneOptions{})
}

func (s *Server) serveClientDataPlaneWith(w http.ResponseWriter, r *http.Request, proto ir.Protocol, opts dataPlaneOptions) {
	client, err := route.ParseClientID(r.PathValue("client"))
	if err != nil {
		writeInboundError(w, http.StatusNotFound, proto,
			fmt.Sprintf("unknown client %q", r.PathValue("client")), "client_not_found")
		return
	}
	s.serveDataPlaneWith(w, r, client, proto, opts)
}

// ---- pipeline --------------------------------------------------------------

// serveDataPlane implements the common pipeline: JSON/Content-Type limits,
// routing resolution, adapter dispatch gate, then either the same-protocol
// passthrough or the cross-protocol IR pipeline.
func (s *Server) serveDataPlane(w http.ResponseWriter, r *http.Request, client route.ClientID, inProto ir.Protocol) {
	s.serveDataPlaneWith(w, r, client, inProto, dataPlaneOptions{})
}

func (s *Server) serveDataPlaneWith(w http.ResponseWriter, r *http.Request, client route.ClientID, inProto ir.Protocol, opts dataPlaneOptions) {
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeInboundError(w, http.StatusUnsupportedMediaType, inProto,
			"Content-Type must be application/json", "invalid_content_type")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxChatBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeInboundError(w, http.StatusRequestEntityTooLarge, inProto,
				"request body exceeds 128 MiB", "request_too_large")
			return
		}
		writeInboundError(w, http.StatusBadRequest, inProto, "cannot read request body", "")
		return
	}

	model, stream, err := parseForRouting(inProto, body)
	if err != nil {
		writeInboundError(w, http.StatusBadRequest, inProto, err.Error(), "")
		return
	}

	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeInboundError(w, http.StatusInternalServerError, inProto, "config not loaded", "")
		return
	}
	trace, err := s.startTrace(cfg, client, inProto, r, body)
	if err != nil {
		writeInboundError(w, http.StatusInternalServerError, inProto, err.Error(), "request_log_failed")
		return
	}
	tracked := &responseStatusWriter{ResponseWriter: w}
	w = tracked
	if trace != nil {
		w.Header().Set("X-Request-Id", trace.requestID())
	}
	defer func() { trace.finish(r.Context(), tracked.status) }()
	res, err := route.Resolve(client, model, cfg)
	if err != nil {
		writeInboundError(w, http.StatusBadRequest, inProto, err.Error(), "")
		return
	}
	cfgProvider, ok := cfg.Providers[res.Provider]
	if !ok {
		writeInboundError(w, http.StatusBadRequest, inProto,
			fmt.Sprintf("provider %q does not exist", res.Provider), "")
		return
	}
	outProto := adapterProtocol(cfgProvider.Adapter)
	if outProto == "" {
		writeInboundError(w, http.StatusUnprocessableEntity, inProto,
			fmt.Sprintf("provider %q uses unknown adapter %q", res.Provider, cfgProvider.Adapter),
			"unsupported_adapter")
		return
	}
	if opts.compact {
		// Codex remote compaction is POST /responses/compact. Only the
		// openai-responses adapter can forward that path losslessly; any
		// other adapter would require inventing a compact translation.
		if outProto != ir.ProtocolResponses {
			writeInboundError(w, http.StatusUnprocessableEntity, inProto,
				fmt.Sprintf("remote compaction requires an openai-responses upstream; provider %q uses %q", res.Provider, cfgProvider.Adapter),
				"unsupported_compact")
			return
		}
		stream = false
	}
	if err := trace.route(res.Provider, res.Model, cfgProvider.Adapter, inProto, outProto); err != nil {
		trace.setError(err)
		writeInboundError(w, http.StatusInternalServerError, inProto, err.Error(), "request_log_failed")
		return
	}
	features := inspectRequestFeatures(inProto, body)
	if features.Image && !cfgProvider.Capabilities.ImageInput {
		writeInboundError(w, http.StatusUnprocessableEntity, inProto,
			fmt.Sprintf("provider %q does not support image input", res.Provider), "unsupported_image")
		return
	}
	provider := providerInfo{
		id: res.Provider, baseURL: cfgProvider.BaseURL, secretRef: cfgProvider.SecretRef,
		extraHeaders: mergeInboundAnthropicBeta(cfgProvider.ExtraHeaders, r.Header),
		imageInput:   cfgProvider.Capabilities.ImageInput, reasoning: cfgProvider.Capabilities.Reasoning,
		contextManagement: cfgProvider.Capabilities.ContextManagement,
	}
	if inProto == ir.ProtocolMessages && !provider.contextManagement {
		var dropped bool
		body, dropped, err = messages.DropContextManagement(body)
		if err != nil {
			writeInboundError(w, http.StatusBadRequest, inProto, err.Error(), "")
			return
		}
		if dropped {
			if err := s.writeContextManagementDropped(trace, inProto, outProto, provider.id); err != nil {
				writeInboundError(w, http.StatusInternalServerError, inProto, err.Error(), "warning_log_failed")
				return
			}
		}
	}

	if inProto == outProto {
		if features.Reasoning && !provider.reasoning {
			body, err = dropReasoning(inProto, body)
			if err != nil {
				writeInboundError(w, http.StatusBadRequest, inProto, err.Error(), "")
				return
			}
			if err := s.writeReasoningDropped(trace, inProto, outProto, provider.id, "provider capability is disabled"); err != nil {
				writeInboundError(w, http.StatusInternalServerError, inProto, err.Error(), "warning_log_failed")
				return
			}
		}
		s.serveSameProtocol(w, r, inProto, body, res.Model, stream, provider, trace, opts)
		return
	}
	if opts.compact {
		writeInboundError(w, http.StatusUnprocessableEntity, inProto,
			fmt.Sprintf("remote compaction requires an openai-responses upstream; provider %q uses %q", res.Provider, cfgProvider.Adapter),
			"unsupported_compact")
		return
	}
	s.serveCrossProtocol(w, r, inProto, outProto, body, res.Model, stream, provider, trace)
}

// parseForRouting extracts model and stream for routing without committing
// to any protocol conversion.
func parseForRouting(proto ir.Protocol, body []byte) (string, bool, error) {
	switch proto {
	case ir.ProtocolChat:
		req, err := chat.Parse(body)
		if err != nil {
			return "", false, err
		}
		return req.Model, req.StreamValue(), nil
	case ir.ProtocolResponses:
		req, err := responses.Parse(body)
		if err != nil {
			return "", false, err
		}
		return req.Model, req.StreamValue(), nil
	case ir.ProtocolMessages:
		req, err := messages.Parse(body)
		if err != nil {
			return "", false, err
		}
		return req.Model, req.StreamValue(), nil
	}
	return "", false, fmt.Errorf("unknown protocol %q", proto)
}

// ---- same-protocol passthrough ---------------------------------------------

// serveSameProtocol rewrites only model/stream, rewrites authentication and
// forwards the upstream response at the HTTP level (docs/v1-scheme.md §8.3).
func (s *Server) serveSameProtocol(w http.ResponseWriter, r *http.Request, proto ir.Protocol, body []byte, model string, stream bool, provider providerInfo, trace *requestTrace, opts dataPlaneOptions) {
	outBody, err := rewriteForProtocol(proto, body, model, stream)
	if err != nil {
		writeInboundError(w, http.StatusBadRequest, proto, err.Error(), "")
		return
	}
	if err := trace.upstreamRequest(proto, provider, outBody, stream, opts.compact); err != nil {
		trace.setError(err)
		writeInboundError(w, http.StatusInternalServerError, proto, err.Error(), "request_log_failed")
		return
	}
	upResp, err := s.upstreamDo(r.Context(), proto, provider, outBody, stream, opts.compact)
	if err != nil {
		trace.setError(err)
		s.writeUpstreamError(w, proto, err)
		return
	}
	defer upResp.Body.Close()
	// 同协议：保留上游状态码与错误体（4xx/5xx 原样）。
	forwardHeadersOnly(w, upResp)
	w.WriteHeader(upResp.StatusCode)
	s.pipeBody(w, upResp, proto, stream, trace)
}

// ---- cross-protocol IR pipeline --------------------------------------------

func (s *Server) serveCrossProtocol(w http.ResponseWriter, r *http.Request, inProto, outProto ir.Protocol, body []byte, model string, stream bool, provider providerInfo, trace *requestTrace) {
	req, err := parseRequestIR(inProto, body)
	if err != nil {
		if errors.Is(err, ir.ErrUnsupportedContent) {
			writeInboundError(w, http.StatusUnprocessableEntity, inProto,
				err.Error(), "unsupported_content")
			return
		}
		writeInboundError(w, http.StatusBadRequest, inProto, err.Error(), "")
		return
	}
	req.Model = model
	req.Client = ir.ClientID(provider.id)
	req.Protocol = inProto

	dropped, reason := normalizeReasoning(req, outProto, provider.reasoning)
	if dropped {
		cfg := s.cfg.Snapshot()
		if cfg == nil {
			writeInboundError(w, http.StatusInternalServerError, inProto, "config not loaded", "")
			return
		}
		if err := s.writeReasoningDropped(trace, inProto, outProto, provider.id, reason); err != nil {
			writeInboundError(w, http.StatusInternalServerError, inProto, err.Error(), "warning_log_failed")
			return
		}
	}
	if len(req.DroppedTools) > 0 {
		if err := s.writeToolDropped(trace, inProto, outProto, provider.id, req.DroppedTools); err != nil {
			writeInboundError(w, http.StatusInternalServerError, inProto, err.Error(), "warning_log_failed")
			return
		}
	}

	outBody, err := generateRequest(outProto, req)
	if err != nil {
		if errors.Is(err, ir.ErrUnsupportedContent) {
			writeInboundError(w, http.StatusUnprocessableEntity, inProto,
				err.Error(), "unsupported_content")
			return
		}
		writeInboundError(w, http.StatusInternalServerError, inProto, err.Error(), "")
		return
	}
	if err := trace.upstreamRequest(outProto, provider, outBody, stream, false); err != nil {
		trace.setError(err)
		writeInboundError(w, http.StatusInternalServerError, inProto, err.Error(), "request_log_failed")
		return
	}

	upResp, err := s.upstreamDo(r.Context(), outProto, provider, outBody, stream, false)
	if err != nil {
		trace.setError(err)
		s.writeUpstreamError(w, inProto, err)
		return
	}
	defer upResp.Body.Close()

	if upResp.StatusCode >= 400 {
		// 跨协议：把上游错误转成入站协议原生错误外形，尽量保留状态码。
		errorBody, readErr := io.ReadAll(upResp.Body)
		if readErr != nil {
			trace.setError(readErr)
		}
		trace.upstreamEvent(rawJSONOrString(errorBody))
		msg := upstreamErrorMessage(strings.NewReader(string(errorBody)))
		trace.setError(errors.New(msg))
		writeInboundError(w, upResp.StatusCode, inProto, msg, "upstream_error")
		return
	}

	customNames := ir.CustomToolNames(req.Tools)
	if stream {
		s.streamCrossProtocol(w, inProto, outProto, model, upResp, trace, customNames)
		return
	}
	s.nonStreamCrossProtocol(w, inProto, outProto, model, upResp, trace, customNames)
}

// streamCrossProtocol pipes the upstream SSE stream (decoded with the
// upstream protocol's reader) through the IR event sequencer into the
// inbound protocol's SSE encoding, flushing after every event. A broken
// upstream stream ends with a protocol error event, never a fabricated
// success.
func (s *Server) streamCrossProtocol(w http.ResponseWriter, inProto, outProto ir.Protocol, model string, upResp *http.Response, trace *requestTrace, customNames map[string]bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	collector := &usageCollector{}
	upEvents := &sseEventLogger{trace: trace, upstream: true}
	reader := newOutStreamReader(outProto, &usageTrackingReader{r: upResp.Body, c: collector, events: upEvents})
	seq := ir.NewSequencer()
	customIDs := map[string]bool{}
	next := func() (ir.Event, error) {
		ev, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				upEvents.finish()
				if collector.found {
					trace.setUsage(collector.usage)
				}
				return ir.Event{}, io.EOF
			}
			return ir.Event{Type: ir.EventError, Error: &ir.ErrorInfo{
				Type: "api_error", Message: "upstream stream error: " + err.Error(),
			}}, nil
		}
		ev = ir.MarkCustomToolEvent(ev, customNames, customIDs)
		if ev.Type == ir.EventUsage {
			trace.setIRUsage(ev.Usage)
		}
		if perr := seq.Push(ev); perr != nil {
			return ir.Event{Type: ir.EventError, Error: &ir.ErrorInfo{
				Type: "api_error", Message: perr.Error(),
			}}, nil
		}
		return ev, nil
	}
	clientEvents := &sseEventLogger{trace: trace, client: true}
	tw := &traceClientWriter{w: w, events: clientEvents}
	if err := encodeStream(inProto, tw, flusher.Flush, model, next); err != nil {
		trace.setError(err)
	}
	clientEvents.finish()
}

// nonStreamCrossProtocol parses the upstream JSON into ir events, validates
// them in the sequencer and encodes the aggregated response in the inbound
// protocol.
func (s *Server) nonStreamCrossProtocol(w http.ResponseWriter, inProto, outProto ir.Protocol, model string, upResp *http.Response, trace *requestTrace, customNames map[string]bool) {
	upBody, err := io.ReadAll(upResp.Body)
	if err != nil {
		writeInboundError(w, http.StatusBadGateway, inProto, "cannot read upstream response", "")
		return
	}
	trace.upstreamEvent(rawJSONOrString(upBody))
	events, err := parseResponseIR(outProto, upBody)
	if err != nil {
		writeInboundError(w, http.StatusBadGateway, inProto, err.Error(), "")
		return
	}
	seq := ir.NewSequencer()
	for _, ev := range events {
		if perr := seq.Push(ev); perr != nil {
			writeInboundError(w, http.StatusBadGateway, inProto, perr.Error(), "")
			return
		}
	}
	agg := seq.Accumulate()
	ir.MarkCustomToolCalls(agg.ToolCalls, customNames)
	trace.setIRUsage(agg.Usage)
	if agg.Errored {
		writeInboundError(w, http.StatusBadGateway, inProto, "upstream failed", "upstream_error")
		return
	}
	if !agg.Completed {
		writeInboundError(w, http.StatusBadGateway, inProto, "upstream response incomplete", "upstream_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var clientBody strings.Builder
	if err := encodeNonStream(inProto, &clientBody, model, agg); err != nil {
		trace.setError(err)
		writeInboundError(w, http.StatusInternalServerError, inProto, err.Error(), "")
		return
	}
	encoded := []byte(clientBody.String())
	trace.clientEvent(rawJSONOrString(encoded))
	_, _ = w.Write(encoded)
}

// upstreamErrorMessage extracts a human-readable message from an upstream
// error body without echoing secrets.
func upstreamErrorMessage(r io.Reader) string {
	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return "upstream error"
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &envelope)
	if envelope.Error.Message != "" {
		return envelope.Error.Message
	}
	if envelope.Message != "" {
		return envelope.Message
	}
	return "upstream error"
}

// ---- dispatch helpers ------------------------------------------------------

// providerInfo is the subset of config.Provider the data plane needs.
type providerInfo struct {
	id                string
	baseURL           string
	secretRef         string
	extraHeaders      map[string]string
	imageInput        bool
	reasoning         bool
	contextManagement bool
}

// upstreamDo dispatches to the adapter pool matching the wire protocol.
func (s *Server) upstreamDo(ctx context.Context, proto ir.Protocol, p providerInfo, body []byte, stream bool, compact bool) (*http.Response, error) {
	switch proto {
	case ir.ProtocolChat:
		return s.upstreamsChat.Client(p.baseURL, p.secretRef).DoWithHeaders(ctx, body, stream, p.extraHeaders)
	case ir.ProtocolResponses:
		client := s.upstreamsResponses.Client(p.baseURL, p.secretRef)
		if compact {
			return client.DoCompactWithHeaders(ctx, body, p.extraHeaders)
		}
		return client.DoWithHeaders(ctx, body, stream, p.extraHeaders)
	case ir.ProtocolMessages:
		return s.upstreamsAnthropic.Client(p.baseURL, p.secretRef).DoWithHeaders(ctx, body, stream, p.extraHeaders)
	}
	return nil, fmt.Errorf("unknown upstream protocol %q", proto)
}

// writeUpstreamError maps upstream failures: missing/unreadable secret →
// 500, response-header timeout → 504, anything else → 502.
func (s *Server) writeUpstreamError(w http.ResponseWriter, proto ir.Protocol, err error) {
	var urlErr *url.Error
	switch {
	case errors.Is(err, openaichat.ErrSecretMissing), errors.Is(err, openaichat.ErrSecretStore),
		errors.Is(err, anthropic.ErrSecretMissing), errors.Is(err, anthropic.ErrSecretStore),
		errors.Is(err, openairesponses.ErrSecretMissing), errors.Is(err, openairesponses.ErrSecretStore):
		writeInboundError(w, http.StatusInternalServerError, proto, err.Error(), "")
	case errors.As(err, &urlErr) && urlErr.Timeout():
		writeInboundError(w, http.StatusGatewayTimeout, proto, "upstream did not respond in time", "")
	default:
		writeInboundError(w, http.StatusBadGateway, proto, "upstream unreachable: "+err.Error(), "")
	}
}

// forwardHeadersOnly copies the whitelisted upstream headers onto w.
func forwardHeadersOnly(w http.ResponseWriter, upResp *http.Response) {
	for _, h := range forwardHeaders {
		for _, v := range upResp.Header.Values(h) {
			w.Header().Add(h, v)
		}
	}
}

// pipeBody writes the upstream body; streaming responses flush after every
// chunk, non-streaming responses are copied through.
func (s *Server) pipeBody(w http.ResponseWriter, upResp *http.Response, proto ir.Protocol, stream bool, trace *requestTrace) {
	if !stream {
		body, err := io.ReadAll(upResp.Body)
		if err != nil {
			trace.setError(err)
			return
		}
		trace.upstreamEvent(rawJSONOrString(body))
		trace.clientEvent(rawJSONOrString(body))
		if usage, ok := usageFromJSON(body); ok {
			trace.setUsage(usage)
		}
		_, _ = w.Write(body)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		_, _ = io.Copy(w, upResp.Body)
		return
	}
	buf := make([]byte, 32*1024)
	collector := &usageCollector{}
	events := &sseEventLogger{trace: trace, upstream: true, client: true}
	for {
		n, err := upResp.Body.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			events.feed(chunk)
			collector.feed(chunk)
			if _, werr := w.Write(buf[:n]); werr != nil {
				trace.setError(werr)
				return
			}
			flusher.Flush()
		}
		if err != nil {
			events.finish()
			if collector.found {
				trace.setUsage(collector.usage)
			}
			if err != io.EOF {
				trace.setError(err)
			}
			return
		}
	}
}

// ---- protocol plumbing (inbound side) --------------------------------------

func rewriteForProtocol(proto ir.Protocol, body []byte, model string, stream bool) ([]byte, error) {
	switch proto {
	case ir.ProtocolChat:
		req, err := chat.Parse(body)
		if err != nil {
			return nil, err
		}
		return req.Rewrite(model, stream)
	case ir.ProtocolResponses:
		req, err := responses.Parse(body)
		if err != nil {
			return nil, err
		}
		return req.Rewrite(model, stream)
	case ir.ProtocolMessages:
		req, err := messages.Parse(body)
		if err != nil {
			return nil, err
		}
		return req.Rewrite(model, stream)
	}
	return nil, fmt.Errorf("unknown protocol %q", proto)
}

func inspectRequestFeatures(proto ir.Protocol, body []byte) ir.RequestFeatures {
	switch proto {
	case ir.ProtocolChat:
		return chat.InspectFeatures(body)
	case ir.ProtocolResponses:
		return responses.InspectFeatures(body)
	case ir.ProtocolMessages:
		return messages.InspectFeatures(body)
	}
	return ir.RequestFeatures{}
}

func dropReasoning(proto ir.Protocol, body []byte) ([]byte, error) {
	switch proto {
	case ir.ProtocolChat:
		return chat.DropReasoning(body)
	case ir.ProtocolResponses:
		return responses.DropReasoning(body)
	case ir.ProtocolMessages:
		return messages.DropReasoning(body)
	}
	return nil, fmt.Errorf("unknown protocol %q", proto)
}

// normalizeReasoning keeps only reasoning semantics that have a direct,
// documented target representation. OpenAI Chat and Responses share effort;
// Anthropic thinking modes and budgets do not have a lossless OpenAI mapping.
func normalizeReasoning(req *ir.Request, outProto ir.Protocol, providerSupports bool) (bool, string) {
	droppedBlocks := false
	keptMessages := make([]ir.Message, 0, len(req.Messages))
	for _, msg := range req.Messages {
		kept := make([]ir.Block, 0, len(msg.Content))
		droppedFromMsg := false
		for _, block := range msg.Content {
			if block.Type == ir.BlockReasoning {
				droppedBlocks = true
				droppedFromMsg = true
				continue
			}
			kept = append(kept, block)
		}
		msg.Content = kept
		// A reasoning-only item becomes an empty assistant message. Chat
		// Completions then treats it as breaking the tool_calls / tool pair
		// (docs/v1-scheme.md §8.4, §20 2026-08-16).
		if droppedFromMsg && len(kept) == 0 {
			continue
		}
		keptMessages = append(keptMessages, msg)
	}
	req.Messages = keptMessages

	hadConfig := !req.Reasoning.Empty()
	if !providerSupports {
		req.Reasoning = ir.ReasoningConfig{}
		if hadConfig || droppedBlocks {
			return true, "provider capability is disabled"
		}
		return false, ""
	}

	droppedConfig := false
	switch req.Reasoning.Source {
	case ir.ProtocolChat, ir.ProtocolResponses:
		if outProto != ir.ProtocolChat && outProto != ir.ProtocolResponses {
			req.Reasoning = ir.ReasoningConfig{}
			droppedConfig = hadConfig
		} else if outProto == ir.ProtocolChat && req.Reasoning.Summary != "" {
			req.Reasoning.Summary = ""
			droppedConfig = true
		}
	case ir.ProtocolMessages:
		if outProto != ir.ProtocolMessages {
			req.Reasoning = ir.ReasoningConfig{}
			droppedConfig = hadConfig
		}
	}
	if droppedBlocks || droppedConfig {
		return true, "target protocol cannot represent the reasoning semantics without loss"
	}
	return false, ""
}

func (s *Server) writeReasoningDropped(trace *requestTrace, inProto, outProto ir.Protocol, providerID, reason string) error {
	if trace == nil {
		return nil
	}
	if err := trace.session.Append("warning", map[string]any{
		"code":    "reasoning_dropped",
		"message": "reasoning content or configuration was removed before the upstream request",
		"details": map[string]any{
			"inbound_protocol":  inProto,
			"outbound_protocol": outProto,
			"provider":          providerID,
			"reason":            reason,
		}}); err != nil {
		return fmt.Errorf("write reasoning downgrade warning: %w", err)
	}
	return nil
}

func (s *Server) writeToolDropped(trace *requestTrace, inProto, outProto ir.Protocol, providerID string, dropped []ir.DroppedTool) error {
	if trace == nil {
		return nil
	}
	details := make([]map[string]any, 0, len(dropped))
	for _, tool := range dropped {
		details = append(details, map[string]any{
			"type":   tool.Type,
			"name":   tool.Name,
			"reason": tool.Reason,
		})
	}
	if err := trace.session.Append("warning", map[string]any{
		"code":    "tool_dropped",
		"message": "hosted tools or custom-tool formats were removed or downgraded before the upstream request",
		"details": map[string]any{
			"inbound_protocol":  inProto,
			"outbound_protocol": outProto,
			"provider":          providerID,
			"tools":             details,
		},
	}); err != nil {
		return fmt.Errorf("write tool downgrade warning: %w", err)
	}
	return nil
}

func (s *Server) writeContextManagementDropped(trace *requestTrace, inProto, outProto ir.Protocol, providerID string) error {
	if trace == nil {
		return nil
	}
	if err := trace.session.Append("warning", map[string]any{
		"code":    "context_management_dropped",
		"message": "context_management was removed before the upstream request because the provider capability is disabled",
		"details": map[string]any{
			"inbound_protocol":  inProto,
			"outbound_protocol": outProto,
			"provider":          providerID,
		},
	}); err != nil {
		return fmt.Errorf("write context management downgrade warning: %w", err)
	}
	return nil
}

func newRequestID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "req_" + hex.EncodeToString(raw[:]), nil
}

func parseRequestIR(proto ir.Protocol, body []byte) (*ir.Request, error) {
	switch proto {
	case ir.ProtocolChat:
		return chat.ParseRequest(body)
	case ir.ProtocolResponses:
		return responses.ParseRequest(body)
	case ir.ProtocolMessages:
		return messages.ParseRequest(body)
	}
	return nil, fmt.Errorf("unknown protocol %q", proto)
}

func generateRequest(proto ir.Protocol, req *ir.Request) ([]byte, error) {
	switch proto {
	case ir.ProtocolChat:
		return openaichat.GenerateRequest(req)
	case ir.ProtocolResponses:
		return openairesponses.GenerateRequest(req)
	case ir.ProtocolMessages:
		return anthropic.GenerateRequest(req)
	}
	return nil, fmt.Errorf("unknown protocol %q", proto)
}

func parseResponseIR(proto ir.Protocol, body []byte) ([]ir.Event, error) {
	switch proto {
	case ir.ProtocolChat:
		return openaichat.ParseResponse(body)
	case ir.ProtocolResponses:
		return openairesponses.ParseResponse(body)
	case ir.ProtocolMessages:
		return anthropic.ParseResponse(body)
	}
	return nil, fmt.Errorf("unknown protocol %q", proto)
}

func newOutStreamReader(proto ir.Protocol, r io.Reader) interface{ Next() (ir.Event, error) } {
	switch proto {
	case ir.ProtocolChat:
		return openaichat.NewStreamReader(r)
	case ir.ProtocolResponses:
		return openairesponses.NewStreamReader(r)
	case ir.ProtocolMessages:
		return anthropic.NewStreamReader(r)
	}
	return nil
}

func encodeStream(proto ir.Protocol, w io.Writer, flush func(), model string, next func() (ir.Event, error)) error {
	switch proto {
	case ir.ProtocolChat:
		return chat.EncodeStream(w, flush, model, next)
	case ir.ProtocolResponses:
		return responses.EncodeStream(w, flush, model, next)
	case ir.ProtocolMessages:
		return messages.EncodeStream(w, flush, model, next)
	}
	return fmt.Errorf("unknown protocol %q", proto)
}

func encodeNonStream(proto ir.Protocol, w io.Writer, model string, resp *ir.Response) error {
	switch proto {
	case ir.ProtocolChat:
		return chat.EncodeNonStream(w, model, resp)
	case ir.ProtocolResponses:
		return responses.EncodeNonStream(w, model, resp)
	case ir.ProtocolMessages:
		return messages.EncodeNonStream(w, model, resp)
	}
	return fmt.Errorf("unknown protocol %q", proto)
}

// ---- models -----------------------------------------------------------------

// modelItem is one entry of GET /v1/models.
//
// display_name is the selectable id (gateway-default or
// <provider-id>/<model-id>) so client pickers show that form. On
// /c/claude/v1/models the wire id is a picker alias that passes Claude
// Code's discovery filter; display_name stays the real selectable id
// (docs/v1-scheme.md §7.5). Everywhere else id equals display_name.
type modelItem struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy     string `json:"owned_by"`
	DisplayName string `json:"display_name"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	s.serveModels(w, r, route.Generic)
}

func (s *Server) handleModelsClient(w http.ResponseWriter, r *http.Request) {
	client, err := route.ParseClientID(r.PathValue("client"))
	if err != nil {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("unknown client %q", r.PathValue("client")), "client_not_found")
		return
	}
	s.serveModels(w, r, client)
}

func (s *Server) serveModels(w http.ResponseWriter, _ *http.Request, client route.ClientID) {
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "config not loaded", "")
		return
	}
	data := s.modelCatalog(cfg)
	if client == route.Claude {
		data = claudePickerCatalog(data)
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// claudePickerCatalog rewrites only the wire id so Claude Code's
// /(claude|anthropic)/i discovery filter keeps every enabled model.
// display_name stays the real selectable id. Decode happens in route.Resolve.
func claudePickerCatalog(items []modelItem) []modelItem {
	out := make([]modelItem, len(items))
	for i, item := range items {
		out[i] = item
		out[i].ID = route.ClaudePickerID(item.ID)
	}
	return out
}

// modelCatalog is the single source of the enabled model list: the reserved
// model first, then every enabled `<provider-id>/<model-id>` (§7.5). Both
// GET /v1/models and the catalog written into client configurations by
// internal/point are built from it, so a client picker and the data plane can
// never disagree about which models exist.
func (s *Server) modelCatalog(cfg *config.Config) []modelItem {
	ids := make([]string, 0, len(cfg.Providers))
	for id := range cfg.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	data := []modelItem{{ID: route.ReservedModel, Object: "model", OwnedBy: "ai-gateway", DisplayName: route.ReservedModel}}
	seen := map[string]bool{route.ReservedModel: true}
	add := func(modelID, providerID string) {
		if seen[modelID] {
			return
		}
		seen[modelID] = true
		data = append(data, modelItem{ID: modelID, Object: "model", OwnedBy: providerID, DisplayName: modelID})
	}
	for _, id := range ids {
		p := cfg.Providers[id]
		if !p.EnabledValue() {
			continue
		}
		if providerModelEnabled(p, p.DefaultModel) {
			add(id+"/"+p.DefaultModel, id)
		}
		for _, model := range p.Models {
			if !model.EnabledValue() {
				continue
			}
			add(id+"/"+model.ID, id)
		}
	}
	cache := s.cachedModels()
	for _, id := range ids {
		if !cfg.Providers[id].EnabledValue() {
			continue
		}
		for _, model := range cache[id] {
			if !providerModelEnabled(cfg.Providers[id], model.RawID) {
				continue
			}
			add(model.ID, id)
		}
	}
	return data
}

func providerModelEnabled(provider config.Provider, modelID string) bool {
	for _, model := range provider.Models {
		if model.ID == modelID {
			return model.EnabledValue()
		}
	}
	return true
}
