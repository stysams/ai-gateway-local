package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/ir"
	"ai-gateway/internal/logstore"
	"ai-gateway/internal/outbound/anthropic"
	"ai-gateway/internal/outbound/openaichat"
	"ai-gateway/internal/outbound/openairesponses"
	"ai-gateway/internal/route"
)

type requestTrace struct {
	session *logstore.Session
	start   time.Time
	usage   *logstore.TokenUsage
	errText string
}

func (s *Server) startTrace(cfg *config.Config, client route.ClientID, proto ir.Protocol, r *http.Request, body []byte) (*requestTrace, error) {
	if !cfg.Logging.EnabledValue() {
		return nil, nil
	}
	requestID, err := newRequestID()
	if err != nil {
		return nil, fmt.Errorf("create request id: %w", err)
	}
	start := time.Now()
	session, err := s.warnings.Open(cfg.Logging.Dir, requestID, start)
	if err != nil {
		return nil, err
	}
	t := &requestTrace{session: session, start: start}
	fields := requestLogFields(r)
	fields["protocol"] = proto
	fields["client"] = client
	fields["body"] = json.RawMessage(append([]byte(nil), body...))
	if err := session.Append("request", fields); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *requestTrace) requestID() string {
	if t == nil || t.session == nil {
		return ""
	}
	return t.sessionRequestID()
}

// sessionRequestID is read from the event contract without exposing Session
// internals through the public logstore API.
func (t *requestTrace) sessionRequestID() string {
	// The id is needed only for the response header. Session.PathID keeps the
	// storage path private while exposing the validated identifier.
	return t.session.RequestID()
}

func (t *requestTrace) route(provider, model, adapter string) error {
	if t == nil {
		return nil
	}
	return t.session.Append("route", map[string]any{"provider": provider, "model": model, "adapter": adapter})
}

func (t *requestTrace) upstreamRequest(proto ir.Protocol, p providerInfo, body []byte, stream bool) error {
	if t == nil {
		return nil
	}
	var endpoint string
	switch proto {
	case ir.ProtocolChat:
		endpoint = openaichat.CompletionURL(p.baseURL)
	case ir.ProtocolResponses:
		endpoint = openairesponses.CompletionURL(p.baseURL)
	case ir.ProtocolMessages:
		endpoint = anthropic.CompletionURL(p.baseURL)
	}
	fields := map[string]any{
		"method": http.MethodPost, "url": endpoint,
		"headers": upstreamLogHeaders(proto, stream),
		"body":    json.RawMessage(append([]byte(nil), body...)),
	}
	if p.secretRef != "" {
		fields["omitted_sensitive_header_count"] = 1
	}
	return t.session.Append("upstream_request", fields)
}

func requestLogFields(r *http.Request) map[string]any {
	headers, omittedHeaders := safeLogHeaders(r.Header)
	query, omittedQuery := safeLogQuery(r.URL.Query())
	scheme := r.URL.Scheme
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	requestURI := r.URL.EscapedPath()
	if encoded := query.Encode(); encoded != "" {
		requestURI += "?" + encoded
	}
	fields := map[string]any{
		"method": r.Method, "scheme": scheme, "host": r.Host,
		"url": scheme + "://" + r.Host + requestURI, "path": r.URL.Path,
		"request_uri": requestURI, "query": query,
		"proto": r.Proto, "remote_addr": r.RemoteAddr, "headers": headers,
		"content_length": r.ContentLength, "transfer_encoding": append([]string(nil), r.TransferEncoding...), "close": r.Close,
	}
	if r.URL.RawPath != "" {
		fields["raw_path"] = r.URL.RawPath
	}
	if len(r.Trailer) > 0 {
		trailers, omittedTrailers := safeLogHeaders(r.Trailer)
		fields["trailers"] = trailers
		omittedHeaders += omittedTrailers
	}
	if omittedHeaders > 0 {
		fields["omitted_sensitive_header_count"] = omittedHeaders
	}
	if omittedQuery > 0 {
		fields["omitted_sensitive_query_count"] = omittedQuery
	}
	return fields
}

func safeLogHeaders(source http.Header) (http.Header, int) {
	result := make(http.Header, len(source))
	omitted := 0
	for name, values := range source {
		if sensitiveLogName(name) {
			omitted++
			continue
		}
		result[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
	return result, omitted
}

func safeLogQuery(source url.Values) (url.Values, int) {
	result := make(url.Values, len(source))
	omitted := 0
	for name, values := range source {
		if sensitiveLogName(name) {
			omitted++
			continue
		}
		result[name] = append([]string(nil), values...)
	}
	return result, omitted
}

func sensitiveLogName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	compact := strings.NewReplacer("-", "", ".", "", "[", "", "]", "").Replace(normalized)
	switch normalized {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key":
		return true
	}
	switch compact {
	case "apikey", "token", "accesstoken", "authtoken", "idtoken", "refreshtoken", "session", "sessionid", "sessiontoken", "password", "passwd":
		return true
	}
	return strings.Contains(normalized, "access-token") ||
		strings.Contains(normalized, "auth-token") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "secret")
}

func upstreamLogHeaders(proto ir.Protocol, stream bool) http.Header {
	headers := http.Header{
		"Content-Type": {"application/json"},
		"User-Agent":   {"ai-gateway"},
	}
	if stream {
		headers["Accept"] = []string{"text/event-stream"}
	} else {
		headers["Accept"] = []string{"application/json"}
	}
	if proto == ir.ProtocolMessages {
		headers["Anthropic-Version"] = []string{anthropic.APIVersion}
	}
	return headers
}

func (t *requestTrace) upstreamEvent(value any) {
	if t != nil {
		_ = t.session.Append("upstream_event", map[string]any{"event": value})
	}
}

func (t *requestTrace) clientEvent(value any) {
	if t != nil {
		_ = t.session.Append("client_event", map[string]any{"event": value})
	}
}

func (t *requestTrace) setIRUsage(usage ir.Usage) {
	if t == nil {
		return
	}
	t.usage = &logstore.TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, ReasoningTokens: usage.ReasoningTokens, TotalTokens: usage.TotalTokens}
}

func (t *requestTrace) setUsage(usage logstore.TokenUsage) {
	if t != nil {
		t.usage = &usage
	}
}

func (t *requestTrace) setError(err error) {
	if t != nil && err != nil {
		t.errText = err.Error()
	}
}

func (t *requestTrace) finish(ctx context.Context, statusCode int) {
	if t == nil {
		return
	}
	status := "success"
	if ctx.Err() != nil {
		status = "cancelled"
		t.errText = ctx.Err().Error()
	} else if statusCode >= 400 || statusCode == 0 || t.errText != "" {
		status = "failed"
	}
	fields := map[string]any{
		"status": status, "status_code": statusCode,
		"duration_ms": time.Since(t.start).Milliseconds(), "completed_at": time.Now(),
	}
	if t.usage != nil {
		fields["usage"] = t.usage
	} else {
		fields["usage"] = nil
	}
	if t.errText == "" && status != "success" {
		t.errText = fmt.Sprintf("HTTP status %d", statusCode)
	}
	if t.errText != "" {
		fields["error"] = t.errText
	}
	_ = t.session.Append("result", fields)
}

type responseStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseStatusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseStatusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *responseStatusWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *responseStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type traceClientWriter struct {
	w      http.ResponseWriter
	events *sseEventLogger
}

func (w *traceClientWriter) Header() http.Header    { return w.w.Header() }
func (w *traceClientWriter) WriteHeader(status int) { w.w.WriteHeader(status) }
func (w *traceClientWriter) Write(p []byte) (int, error) {
	w.events.feed(p)
	return w.w.Write(p)
}

// usageCollector extracts only explicit upstream usage from JSON or SSE.
type usageCollector struct {
	buf   []byte
	usage logstore.TokenUsage
	found bool
}

type usageTrackingReader struct {
	r      io.Reader
	c      *usageCollector
	events *sseEventLogger
}

func (r *usageTrackingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.c.feed(p[:n])
		r.events.feed(p[:n])
	}
	return n, err
}

type sseEventLogger struct {
	trace    *requestTrace
	upstream bool
	client   bool
	buf      []byte
}

func (l *sseEventLogger) feed(p []byte) {
	if l == nil || l.trace == nil || len(p) == 0 {
		return
	}
	l.buf = append(l.buf, p...)
	for {
		end := bytes.Index(l.buf, []byte("\n\n"))
		sep := 2
		crlf := bytes.Index(l.buf, []byte("\r\n\r\n"))
		if crlf >= 0 && (end < 0 || crlf < end) {
			end, sep = crlf, 4
		}
		if end < 0 {
			return
		}
		l.emit(l.buf[:end])
		l.buf = l.buf[end+sep:]
	}
}

func (l *sseEventLogger) finish() {
	if l == nil {
		return
	}
	if len(bytes.TrimSpace(l.buf)) > 0 {
		l.emit(l.buf)
	}
	l.buf = nil
}

func (l *sseEventLogger) emit(frame []byte) {
	value := string(bytes.TrimSpace(frame))
	if value == "" {
		return
	}
	if l.upstream {
		l.trace.upstreamEvent(value)
	}
	if l.client {
		l.trace.clientEvent(value)
	}
}

func (c *usageCollector) feed(p []byte) {
	c.buf = append(c.buf, p...)
	for {
		i := bytes.IndexByte(c.buf, '\n')
		if i < 0 {
			return
		}
		line := bytes.TrimSpace(c.buf[:i])
		c.buf = c.buf[i+1:]
		if bytes.HasPrefix(line, []byte("data:")) {
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if !bytes.Equal(data, []byte("[DONE]")) {
				c.consume(data)
			}
		}
	}
}

func (c *usageCollector) consume(data []byte) {
	usage, ok := explicitUsage(data)
	if !ok {
		return
	}
	c.found = true
	if usage.InputTokens != 0 || c.usage.InputTokens == 0 {
		c.usage.InputTokens = usage.InputTokens
	}
	if usage.OutputTokens != 0 || c.usage.OutputTokens == 0 {
		c.usage.OutputTokens = usage.OutputTokens
	}
	if usage.ReasoningTokens != 0 || c.usage.ReasoningTokens == 0 {
		c.usage.ReasoningTokens = usage.ReasoningTokens
	}
	if usage.TotalTokens != 0 {
		c.usage.TotalTokens = usage.TotalTokens
	} else {
		c.usage.TotalTokens = c.usage.InputTokens + c.usage.OutputTokens
	}
}

func explicitUsage(data []byte) (logstore.TokenUsage, bool) {
	var root map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return logstore.TokenUsage{}, false
	}
	usage, ok := mapAt(root, "usage")
	if !ok {
		for _, container := range []string{"response", "message"} {
			if nested, exists := mapAt(root, container); exists {
				if usage, ok = mapAt(nested, "usage"); ok {
					break
				}
			}
		}
	}
	if !ok {
		return logstore.TokenUsage{}, false
	}
	input, hasInput := firstInt(usage, "input_tokens", "prompt_tokens")
	output, hasOutput := firstInt(usage, "output_tokens", "completion_tokens")
	total, hasTotal := firstInt(usage, "total_tokens")
	reasoning := int64(0)
	for _, detailsKey := range []string{"output_tokens_details", "completion_tokens_details"} {
		if details, exists := mapAt(usage, detailsKey); exists {
			reasoning, _ = firstInt(details, "reasoning_tokens")
		}
	}
	if !hasTotal && (hasInput || hasOutput) {
		total = input + output
	}
	return logstore.TokenUsage{InputTokens: input, OutputTokens: output, ReasoningTokens: reasoning, TotalTokens: total}, true
}

func mapAt(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key].(map[string]any)
	return v, ok
}
func firstInt(m map[string]any, keys ...string) (int64, bool) {
	for _, key := range keys {
		if n, ok := m[key].(json.Number); ok {
			v, err := n.Int64()
			return v, err == nil
		}
	}
	return 0, false
}

func usageFromJSON(data []byte) (logstore.TokenUsage, bool) { return explicitUsage(data) }

func rawJSONOrString(data []byte) any {
	trimmed := bytes.TrimSpace(data)
	if json.Valid(trimmed) {
		return json.RawMessage(append([]byte(nil), trimmed...))
	}
	return string(data)
}
