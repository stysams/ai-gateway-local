// Package jsonedit applies the smallest possible byte-level edits to a JSON
// document.
//
// Claude Code's user settings hold the gateway's `env` slots next to
// permissions, hooks, status line and MCP switches the gateway does not own.
// Decoding those settings into a map and re-encoding them reorders every key
// and reflows the whole file; docs/v1-scheme.md §12.4 forbids overwriting
// unrelated `env` variables, and the 2026-08-21 record in §20 shows why the
// rest of the document must survive byte-for-byte too. Every edit here is a
// splice into the original bytes.
package jsonedit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ErrNotObject reports that the named member exists but does not hold a JSON
// object, so its members cannot be merged.
var ErrNotObject = errors.New("JSON member is not an object")

// maxDepth bounds the recursion used to walk nested values so a pathological
// document cannot overflow the goroutine stack. encoding/json enforces the
// same limit while tokenizing.
const maxDepth = 10000

// KV is one key and its string value inside an object.
type KV struct {
	Key   string
	Value string
}

type member struct {
	key       string
	indent    string
	valueFrom int
	valueTo   int
	nested    *object
}

type object struct {
	openAt  int
	closeAt int
	members []member
}

type splice struct {
	from int
	to   int
	seq  int
	text string
}

// SetObjectStrings merges kvs into the object held by the root object's member,
// creating that member when it is absent. Members the caller does not name keep
// their original bytes, order and formatting.
func SetObjectStrings(data []byte, name string, kvs []KV) ([]byte, error) {
	newline := detectNewline(data)
	if len(bytes.TrimSpace(data)) == 0 {
		return []byte("{" + newline + "  " + encodeString(name) + ": {" + newline + renderMembers("    ", kvs, newline) + newline + "  }" + newline + "}" + newline), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	root, err := scanObject(data, decoder, name, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing JSON data")
	}
	target := root.member(name)
	if target == nil {
		return insertMissingObject(data, root, name, kvs, newline)
	}
	if target.nested == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotObject, name)
	}
	var edits []splice
	var appended []KV
	for _, kv := range kvs {
		existing := target.nested.member(kv.Key)
		if existing == nil {
			appended = append(appended, kv)
			continue
		}
		edits = append(edits, splice{from: existing.valueFrom, to: existing.valueTo, text: encodeString(kv.Value)})
	}
	if len(appended) > 0 {
		edits = append(edits, appendMembers(target.nested, target.indent, appended, newline))
	}
	return apply(data, edits)
}

// detectNewline picks the line ending inserted lines must use, so editing a
// CRLF document does not leave the file with mixed endings.
func detectNewline(data []byte) string {
	if i := bytes.IndexByte(data, '\n'); i > 0 && data[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// appendMembers returns the single splice that adds members to an object.
func appendMembers(target *object, parentIndent string, kvs []KV, newline string) splice {
	indent := target.memberIndent(parentIndent + indentUnit(parentIndent))
	if len(target.members) == 0 {
		return splice{
			from: target.openAt,
			to:   target.closeAt + 1,
			text: "{" + newline + renderMembers(indent, kvs, newline) + newline + parentIndent + "}",
		}
	}
	last := target.members[len(target.members)-1]
	var b strings.Builder
	for _, kv := range kvs {
		b.WriteString("," + newline)
		b.WriteString(indent)
		b.WriteString(encodeString(kv.Key))
		b.WriteString(": ")
		b.WriteString(encodeString(kv.Value))
	}
	return splice{from: last.valueTo, to: last.valueTo, text: b.String()}
}

// insertMissingObject adds an absent member holding kvs to the root object.
func insertMissingObject(data []byte, root *object, name string, kvs []KV, newline string) ([]byte, error) {
	indent := root.memberIndent("  ")
	inner := indent + indentUnit(indent)
	body := encodeString(name) + ": {" + newline + renderMembers(inner, kvs, newline) + newline + indent + "}"
	if len(root.members) == 0 {
		return apply(data, []splice{{
			from: root.openAt,
			to:   root.closeAt + 1,
			text: "{" + newline + indent + body + newline + "}",
		}})
	}
	last := root.members[len(root.members)-1]
	return apply(data, []splice{{
		from: last.valueTo,
		to:   last.valueTo,
		text: "," + newline + indent + body,
	}})
}

func renderMembers(indent string, kvs []KV, newline string) string {
	lines := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		lines = append(lines, indent+encodeString(kv.Key)+": "+encodeString(kv.Value))
	}
	return strings.Join(lines, ","+newline)
}

// scanObject reads one JSON object and records the byte range of every member
// value. The member named want is descended into so its own members can be
// edited; every other value is skipped.
func scanObject(data []byte, decoder *json.Decoder, want string, depth int) (*object, error) {
	if depth > maxDepth {
		return nil, errors.New("JSON document is nested too deeply")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	if token != json.Delim('{') {
		return nil, errors.New("parse JSON: expected an object")
	}
	current := &object{openAt: int(decoder.InputOffset()) - 1}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("parse JSON: %w", err)
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("parse JSON: expected an object key")
		}
		keyEnd := int(decoder.InputOffset())
		from, err := valueStart(data, keyEnd)
		if err != nil {
			return nil, err
		}
		entry := member{key: name, indent: indentAt(data, keyEnd), valueFrom: from}
		if name == want && from < len(data) && data[from] == '{' {
			nested, err := scanObject(data, decoder, "", depth+1)
			if err != nil {
				return nil, err
			}
			entry.nested = nested
		} else if err := skipValue(decoder, depth+1); err != nil {
			return nil, err
		}
		entry.valueTo = int(decoder.InputOffset())
		current.members = append(current.members, entry)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	current.closeAt = int(decoder.InputOffset()) - 1
	return current, nil
}

func skipValue(decoder *json.Decoder, depth int) error {
	if depth > maxDepth {
		return errors.New("JSON document is nested too deeply")
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("parse JSON: %w", err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim == '{' {
		for decoder.More() {
			if _, err := decoder.Token(); err != nil {
				return fmt.Errorf("parse JSON: %w", err)
			}
			if err := skipValue(decoder, depth+1); err != nil {
				return err
			}
		}
	} else {
		for decoder.More() {
			if err := skipValue(decoder, depth+1); err != nil {
				return err
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("parse JSON: %w", err)
	}
	return nil
}

func (o *object) member(name string) *member {
	for i := range o.members {
		if o.members[i].key == name {
			return &o.members[i]
		}
	}
	return nil
}

// memberIndent is the indentation the object's own members are written with,
// falling back to the caller's guess for an object that has none yet.
func (o *object) memberIndent(fallback string) string {
	if len(o.members) > 0 && o.members[0].indent != "" {
		return o.members[0].indent
	}
	return fallback
}

// indentUnit guesses one indentation step from an existing indent.
func indentUnit(indent string) string {
	if indent == "" {
		return "  "
	}
	if strings.HasPrefix(indent, "\t") {
		return "\t"
	}
	return strings.Repeat(" ", len(indent))
}

// valueStart finds the first byte of a member's value, given the offset just
// past its key.
func valueStart(data []byte, keyEnd int) (int, error) {
	i := keyEnd
	for i < len(data) && isSpace(data[i]) {
		i++
	}
	if i >= len(data) || data[i] != ':' {
		return 0, fmt.Errorf("parse JSON: expected ':' after the key at offset %d", keyEnd)
	}
	i++
	for i < len(data) && isSpace(data[i]) {
		i++
	}
	if i >= len(data) {
		return 0, errors.New("parse JSON: member without a value")
	}
	return i, nil
}

// indentAt returns the leading whitespace of the line offset sits on, or the
// empty string when the line holds anything else before it.
func indentAt(data []byte, offset int) string {
	start := offset
	for start > 0 && data[start-1] != '\n' {
		start--
	}
	end := start
	for end < offset && (data[end] == ' ' || data[end] == '\t') {
		end++
	}
	if end == offset {
		return ""
	}
	if data[end] != '"' {
		return ""
	}
	return string(data[start:end])
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func apply(data []byte, edits []splice) ([]byte, error) {
	items := make([]splice, len(edits))
	copy(items, edits)
	for i := range items {
		items[i].seq = i
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].from != items[j].from {
			return items[i].from < items[j].from
		}
		return items[i].seq < items[j].seq
	})
	var out bytes.Buffer
	cursor := 0
	for _, item := range items {
		if item.from < cursor {
			return nil, fmt.Errorf("overlapping JSON edits at offset %d", item.from)
		}
		out.Write(data[cursor:item.from])
		out.WriteString(item.text)
		cursor = item.to
	}
	out.Write(data[cursor:])
	return out.Bytes(), nil
}

// encodeString renders v as a JSON string without HTML escaping, so a URL or a
// model id keeps the bytes the caller passed in.
func encodeString(v string) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		// Encoding a string cannot fail; only unsupported types can.
		panic(err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
