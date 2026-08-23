package logstore

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const RedactionMarker = "[REDACTED]"

var sensitiveValueNames = map[string]struct{}{
	"authorization": {}, "proxyauthorization": {}, "cookie": {}, "setcookie": {},
	"apikey": {}, "xapikey": {}, "token": {}, "accesstoken": {}, "authtoken": {},
	"idtoken": {}, "refreshtoken": {}, "session": {}, "sessionid": {},
	"sessiontoken": {}, "password": {}, "passwd": {}, "secret": {},
	"clientsecret": {}, "filedata": {},
}

var inlineSecretPattern = regexp.MustCompile(`(?i)\b(api[-_]?key|access[-_]?token|auth[-_]?token|id[-_]?token|refresh[-_]?token|password|passwd|secret|session[-_]?token)=([^&\s]+)`)

// RedactValue recursively removes credential-like values while preserving the
// surrounding request and response structure for debugging.
func RedactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if SensitiveName(key) {
				out[key] = RedactionMarker
				continue
			}
			out[key] = RedactValue(child)
		}
		return out
	case http.Header:
		out := make(http.Header, len(typed))
		for key, values := range typed {
			if SensitiveName(key) {
				out[key] = []string{RedactionMarker}
				continue
			}
			out[key] = redactStrings(values)
		}
		return out
	case url.Values:
		out := make(url.Values, len(typed))
		for key, values := range typed {
			if SensitiveName(key) {
				out[key] = []string{RedactionMarker}
				continue
			}
			out[key] = redactStrings(values)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, value := range typed {
			if SensitiveName(key) {
				out[key] = RedactionMarker
			} else {
				out[key] = redactString(value)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = RedactValue(child)
		}
		return out
	case []string:
		return redactStrings(typed)
	case json.RawMessage:
		return RedactRawJSON(typed)
	case string:
		return redactString(typed)
	default:
		return value
	}
}

func redactStrings(values []string) []string {
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = redactString(value)
	}
	return out
}

// SensitiveName applies the same normalization to JSON keys, query names and
// header-like fields, including nested dotted or bracketed names.
func SensitiveName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	compact := strings.NewReplacer("-", "", "_", "", ".", "", "[", "", "]", "").Replace(normalized)
	if _, ok := sensitiveValueNames[compact]; ok {
		return true
	}
	return strings.Contains(compact, "accesstoken") ||
		strings.Contains(compact, "authtoken") ||
		strings.Contains(compact, "password") ||
		strings.Contains(compact, "secret")
}

// RedactRawJSON sanitizes a JSON value. Invalid JSON is returned as a
// redacted string because stream frames are sometimes stored as text.
func RedactRawJSON(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	var value any
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		encoded, _ := json.Marshal(redactString(string(raw)))
		return encoded
	}
	encoded, err := json.Marshal(RedactValue(value))
	if err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return encoded
}

func redactString(value string) string {
	trimmed := strings.TrimSpace(value)
	if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
		return string(RedactRawJSON(json.RawMessage(trimmed)))
	}
	lines := strings.Split(value, "\n")
	changed := false
	for index, line := range lines {
		prefix, payload, ok := strings.Cut(line, "data:")
		if !ok {
			continue
		}
		space := payload[:len(payload)-len(strings.TrimLeft(payload, " \t"))]
		data := strings.TrimSpace(payload)
		if data == "" || data == "[DONE]" || !json.Valid([]byte(data)) {
			continue
		}
		lines[index] = prefix + "data:" + space + string(RedactRawJSON(json.RawMessage(data)))
		changed = true
	}
	if changed {
		value = strings.Join(lines, "\n")
	}
	return inlineSecretPattern.ReplaceAllString(value, `$1=`+RedactionMarker)
}
