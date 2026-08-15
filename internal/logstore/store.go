package logstore

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrInvalidCursor identifies a malformed or stale pagination cursor.
var ErrInvalidCursor = errors.New("invalid cursor")

// RiskNotice is printed once on every headless serve startup.
const RiskNotice = "ai-gateway 的正文日志可能包含提示词、源代码、工具参数、工具结果和图片原文。日志不会自动轮转或脱敏，请仅在可信设备上启用并自行管理磁盘空间。"

// TokenUsage contains only accounting returned by an upstream. The gateway
// never estimates missing values.
type TokenUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens,omitempty"`
	TotalTokens     int64 `json:"total_tokens"`
}

// Session serializes appends to one request file.
type Session struct {
	mu        sync.Mutex
	path      string
	requestID string
}

// RequestID returns the validated request identifier for response metadata.
func (s *Session) RequestID() string {
	if s == nil {
		return ""
	}
	return s.requestID
}

// Open creates or opens the JSONL file for requestID on the local date of at.
func (w *Writer) Open(logDir, requestID string, at time.Time) (*Session, error) {
	if w == nil {
		return nil, errors.New("log writer is nil")
	}
	root, err := w.logRoot(logDir)
	if err != nil {
		return nil, err
	}
	if !safeRequestID(requestID) {
		return nil, fmt.Errorf("invalid request id %q", requestID)
	}
	dir := filepath.Join(root, at.Local().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	path := filepath.Join(dir, requestID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open request log: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("restrict request log permissions: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close request log: %w", err)
	}
	return &Session{path: path, requestID: requestID}, nil
}

// Append adds one independently parseable JSON object to the session.
func (s *Session) Append(eventType string, fields map[string]any) error {
	if s == nil {
		return nil
	}
	event := make(map[string]any, len(fields)+3)
	for k, v := range fields {
		if k != "timestamp" && k != "request_id" && k != "type" {
			event[k] = v
		}
	}
	event["timestamp"] = time.Now()
	event["request_id"] = s.requestID
	event["type"] = eventType
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode %s log event: %w", eventType, err)
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open request log for append: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("append %s log event: %w", eventType, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close request log: %w", err)
	}
	return nil
}

// Summary is the non-body view returned by GET /api/v1/logs.
type Summary struct {
	RequestID   string      `json:"request_id"`
	StartedAt   time.Time   `json:"started_at"`
	CompletedAt *time.Time  `json:"completed_at,omitempty"`
	Client      string      `json:"client,omitempty"`
	Protocol    string      `json:"protocol,omitempty"`
	Provider    string      `json:"provider,omitempty"`
	Model       string      `json:"model,omitempty"`
	Adapter     string      `json:"adapter,omitempty"`
	Status      string      `json:"status"`
	StatusCode  int         `json:"status_code,omitempty"`
	DurationMS  int64       `json:"duration_ms,omitempty"`
	Usage       *TokenUsage `json:"usage,omitempty"`
}

// Query is the supported request-summary filter set.
type Query struct {
	From, To         *time.Time
	Client, Provider string
	Status           string
	Limit            int
	Cursor           string
}

// Page is a cursor-paginated summary response.
type Page struct {
	Items      []Summary `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// List returns newest-first request summaries.
func (w *Writer) List(logDir string, q Query) (Page, error) {
	files, err := w.files(logDir)
	if err != nil {
		return Page{}, err
	}
	items := make([]Summary, 0, len(files))
	for _, path := range files {
		s, _, err := readSummary(path, false)
		if err != nil {
			continue
		}
		if q.From != nil && s.StartedAt.Before(*q.From) || q.To != nil && s.StartedAt.After(*q.To) {
			continue
		}
		if q.Client != "" && s.Client != q.Client || q.Provider != "" && s.Provider != q.Provider || q.Status != "" && s.Status != q.Status {
			continue
		}
		items = append(items, s)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].StartedAt.Equal(items[j].StartedAt) {
			return items[i].RequestID > items[j].RequestID
		}
		return items[i].StartedAt.After(items[j].StartedAt)
	})
	start := 0
	if q.Cursor != "" {
		cursor, err := decodeCursor(q.Cursor)
		if err != nil {
			return Page{}, err
		}
		found := false
		for i := range items {
			if items[i].RequestID == cursor {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return Page{}, fmt.Errorf("%w: does not reference an available log", ErrInvalidCursor)
		}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := Page{Items: items[start:end]}
	if end < len(items) && end > start {
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(items[end-1].RequestID))
	}
	return page, nil
}

// Detail returns every JSONL event as a parsed JSON object.
func (w *Writer) Detail(logDir, requestID string) ([]json.RawMessage, error) {
	if !safeRequestID(requestID) {
		return nil, os.ErrNotExist
	}
	files, err := w.files(logDir)
	if err != nil {
		return nil, err
	}
	for _, path := range files {
		if strings.TrimSuffix(filepath.Base(path), ".jsonl") != requestID {
			continue
		}
		_, events, err := readSummary(path, true)
		return events, err
	}
	return nil, os.ErrNotExist
}

// UsageGroup is one aggregate over actual result events.
type UsageGroup struct {
	Requests   int         `json:"requests"`
	Success    int         `json:"success"`
	Failed     int         `json:"failed"`
	Cancelled  int         `json:"cancelled"`
	Usage      *TokenUsage `json:"usage"`
	Incomplete bool        `json:"incomplete"`
}

// UsageReport groups usage by every dimension required by the v1 contract.
type UsageReport struct {
	Total      UsageGroup             `json:"total"`
	ByProvider map[string]*UsageGroup `json:"by_provider"`
	ByModel    map[string]*UsageGroup `json:"by_model"`
	ByClient   map[string]*UsageGroup `json:"by_client"`
	ByDate     map[string]*UsageGroup `json:"by_date"`
}

// Usage aggregates only usage values present in result events.
func (w *Writer) Usage(logDir string, q Query) (UsageReport, error) {
	q.Limit = 500
	q.Cursor = ""
	files, err := w.files(logDir)
	report := UsageReport{ByProvider: map[string]*UsageGroup{}, ByModel: map[string]*UsageGroup{}, ByClient: map[string]*UsageGroup{}, ByDate: map[string]*UsageGroup{}}
	if err != nil {
		return report, err
	}
	for _, path := range files {
		s, _, err := readSummary(path, false)
		if err != nil || q.From != nil && s.StartedAt.Before(*q.From) || q.To != nil && s.StartedAt.After(*q.To) {
			continue
		}
		if q.Client != "" && s.Client != q.Client || q.Provider != "" && s.Provider != q.Provider || q.Status != "" && s.Status != q.Status {
			continue
		}
		addUsage(&report.Total, s)
		addUsage(group(report.ByProvider, s.Provider), s)
		addUsage(group(report.ByModel, s.Model), s)
		addUsage(group(report.ByClient, s.Client), s)
		addUsage(group(report.ByDate, s.StartedAt.Local().Format("2006-01-02")), s)
	}
	if report.Total.Requests == 0 {
		report.Total.Incomplete = true
	}
	return report, nil
}

func group(m map[string]*UsageGroup, key string) *UsageGroup {
	if key == "" {
		key = "unknown"
	}
	if m[key] == nil {
		m[key] = &UsageGroup{}
	}
	return m[key]
}

func addUsage(g *UsageGroup, s Summary) {
	g.Requests++
	switch s.Status {
	case "success":
		g.Success++
	case "cancelled":
		g.Cancelled++
	default:
		g.Failed++
	}
	if s.Usage == nil {
		g.Incomplete = true
		return
	}
	if g.Usage == nil {
		g.Usage = &TokenUsage{}
	}
	g.Usage.InputTokens += s.Usage.InputTokens
	g.Usage.OutputTokens += s.Usage.OutputTokens
	g.Usage.ReasoningTokens += s.Usage.ReasoningTokens
	g.Usage.TotalTokens += s.Usage.TotalTokens
}

// Inspection is the log portion of the doctor report.
type Inspection struct {
	Enabled          bool   `json:"enabled"`
	Directory        string `json:"directory"`
	Writable         bool   `json:"writable"`
	SizeBytes        int64  `json:"size_bytes"`
	LastParseable    *bool  `json:"last_parseable"`
	InterruptedFiles int    `json:"interrupted_files"`
	Error            string `json:"error,omitempty"`
}

// Inspect checks writability, size, latest-file parsing and interrupted logs.
func (w *Writer) Inspect(logDir string, enabled bool) Inspection {
	result := Inspection{Enabled: enabled}
	root, err := w.logRoot(logDir)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Directory = root
	if err := os.MkdirAll(root, 0o700); err != nil {
		result.Error = err.Error()
		return result
	}
	f, err := os.CreateTemp(root, ".doctor-*.tmp")
	if err == nil {
		result.Writable = true
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
	}
	files, ferr := w.files(logDir)
	if ferr != nil {
		result.Error = ferr.Error()
		return result
	}
	var latest string
	var latestTime time.Time
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		result.SizeBytes += info.Size()
		if info.ModTime().After(latestTime) {
			latest, latestTime = path, info.ModTime()
		}
		s, _, err := readSummary(path, false)
		if err == nil && s.CompletedAt == nil {
			result.InterruptedFiles++
		}
	}
	if latest != "" {
		ok := true
		if _, _, err := readSummary(latest, false); err != nil {
			ok = false
		}
		result.LastParseable = &ok
	}
	return result
}

func (w *Writer) logRoot(logDir string) (string, error) {
	clean := filepath.Clean(logDir)
	if clean == "." || filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("log directory must be a relative path inside the data root")
	}
	target := filepath.Join(w.root, clean)
	if err := ensureExistingAncestorInside(w.root, target); err != nil {
		return "", err
	}
	return target, nil
}

func ensureExistingAncestorInside(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("resolve data root: %w", err)
	}
	ancestor, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	for {
		if _, statErr := os.Lstat(ancestor); statErr == nil {
			break
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fmt.Errorf("log directory has no existing ancestor")
		}
		ancestor = parent
	}
	resolvedAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return fmt.Errorf("resolve log directory: %w", err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedAncestor)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("log directory must stay inside the data root")
	}
	return nil
}

func (w *Writer) files(logDir string) ([]string, error) {
	root, err := w.logRoot(logDir)
	if err != nil {
		return nil, err
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func readSummary(path string, includeEvents bool) (Summary, []json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return Summary{}, nil, err
	}
	defer f.Close()
	s := Summary{RequestID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Status: "interrupted"}
	var events []json.RawMessage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 128<<20)
	seen := false
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var event map[string]json.RawMessage
		if err := json.Unmarshal(line, &event); err != nil {
			return Summary{}, nil, err
		}
		seen = true
		if includeEvents {
			events = append(events, json.RawMessage(line))
		}
		var typ string
		_ = json.Unmarshal(event["type"], &typ)
		var ts time.Time
		_ = json.Unmarshal(event["timestamp"], &ts)
		switch typ {
		case "request":
			s.StartedAt = ts
			_ = json.Unmarshal(event["client"], &s.Client)
			_ = json.Unmarshal(event["protocol"], &s.Protocol)
		case "route":
			_ = json.Unmarshal(event["provider"], &s.Provider)
			_ = json.Unmarshal(event["model"], &s.Model)
			_ = json.Unmarshal(event["adapter"], &s.Adapter)
		case "result":
			s.CompletedAt = &ts
			_ = json.Unmarshal(event["status"], &s.Status)
			_ = json.Unmarshal(event["status_code"], &s.StatusCode)
			_ = json.Unmarshal(event["duration_ms"], &s.DurationMS)
			if raw, ok := event["usage"]; ok && string(raw) != "null" {
				var usage TokenUsage
				if err := json.Unmarshal(raw, &usage); err == nil {
					s.Usage = &usage
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Summary{}, nil, err
	}
	if !seen {
		return Summary{}, nil, io.EOF
	}
	if s.StartedAt.IsZero() {
		info, err := os.Stat(path)
		if err == nil {
			s.StartedAt = info.ModTime()
		}
	}
	return s, events, nil
}

func safeRequestID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func decodeCursor(raw string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || !safeRequestID(string(b)) {
		return "", ErrInvalidCursor
	}
	return string(b), nil
}

// ParseLimit validates a public query limit.
func ParseLimit(raw string) (int, error) {
	if raw == "" {
		return 100, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 || v > 500 {
		return 0, fmt.Errorf("limit must be between 1 and 500")
	}
	return v, nil
}
