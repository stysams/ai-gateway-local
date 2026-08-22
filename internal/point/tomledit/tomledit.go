// Package tomledit applies the smallest possible byte-level edits to a TOML
// document.
//
// A point transaction owns a handful of model and routing keys inside a client
// configuration file. Re-encoding the whole document to write them destroys
// everything the user wrote around them: comments, key order, quoting style and
// the layout of unrelated `[mcp_servers.*]`, `[plugins.*]`, `[projects.*]` and
// other tool tables. docs/v1-scheme.md §12.1 only demands that unknown fields
// survive semantically, but §12.5 additionally requires MCP, plugin, permission
// and UI configuration to be preserved, and the 2026-08-21 record in §20 shows
// what a whole-document re-encode does to a real configuration. Every edit here
// is a splice into the original bytes, so anything the caller does not name
// stays byte-identical.
//
// Targets that cannot be expressed as a splice — a key that lives inside an
// inline table or an array, or a path running through an array of tables —
// report ErrUnsupportedShape so the caller can fall back to re-encoding the
// whole document.
package tomledit

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2/unstable"
)

// ErrUnsupportedShape reports that the requested edit cannot be expressed as a
// byte splice into the original document.
var ErrUnsupportedShape = errors.New("TOML edit target cannot be spliced in place")

// KV is one key and its string value inside a table.
type KV struct {
	Key   string
	Value string
}

type entryKind int

const (
	kindKeyValue entryKind = iota
	kindTable
	kindArrayTable
)

// entry is one top-level TOML expression with the byte ranges needed to edit
// it. path is absolute: a key-value inside `[a.b]` carries `a.b.<key>`.
type entry struct {
	kind      entryKind
	path      []string
	lineStart int // start of the line the expression begins on
	lineEnd   int // end of the expression's last line, trailing newline included
	valueFrom int // key-values only: first byte of the value
	valueTo   int // key-values only: one past the last byte of the value
	blockEnd  int // tables only: end of the last key-value line inside the block
	dropped   bool
}

// splice is one pending edit. An insertion has from == to.
type splice struct {
	from     int
	to       int
	priority int
	seq      int
	text     string
}

// insertion priorities. Root-table keys must land before a table header created
// at the same offset, otherwise they would be parsed as members of that table.
// The blank line separating them sorts after every root key.
const (
	priorityRootKey     = 0
	priorityRootTrailer = 1
	priorityDefault     = 2
)

// Document is a parsed TOML document plus the list of pending splices.
type Document struct {
	data        []byte
	newline     string
	entries     []entry
	splices     []splice
	seq         int
	rootEnd     int // end of the last root-table key-value line, 0 when there is none
	firstTable  int // index of the first table header, -1 when there is none
	lineBreaks  map[int]bool
	rootTrailer bool
}

// detectNewline picks the line ending inserted lines must use, so editing a
// CRLF configuration does not leave the file with mixed endings.
func detectNewline(data []byte) string {
	if i := bytes.IndexByte(data, '\n'); i > 0 && data[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// Parse reads a TOML document. Empty input is a valid empty document.
func Parse(data []byte) (*Document, error) {
	doc := &Document{data: data, newline: detectNewline(data), firstTable: -1, lineBreaks: map[int]bool{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return doc, nil
	}
	p := &unstable.Parser{}
	p.Reset(data)
	var current []string
	table := -1
	for p.NextExpression() {
		node := p.Expression()
		switch node.Kind {
		case unstable.Table, unstable.ArrayTable:
			path, first, _, err := expressionKey(node)
			if err != nil {
				return nil, err
			}
			kind := kindTable
			if node.Kind == unstable.ArrayTable {
				kind = kindArrayTable
			}
			e := entry{kind: kind, path: path, lineStart: lineStart(data, first), lineEnd: lineEnd(data, first)}
			e.blockEnd = e.lineEnd
			doc.entries = append(doc.entries, e)
			if doc.firstTable < 0 {
				doc.firstTable = len(doc.entries) - 1
			}
			table = len(doc.entries) - 1
			current = path
		case unstable.KeyValue:
			relative, _, keyEnd, err := expressionKey(node)
			if err != nil {
				return nil, err
			}
			from := int(node.Raw.Offset)
			to := from + int(node.Raw.Length)
			valueFrom, err := valueStart(data, keyEnd, to)
			if err != nil {
				return nil, err
			}
			path := make([]string, 0, len(current)+len(relative))
			path = append(path, current...)
			path = append(path, relative...)
			e := entry{
				kind:      kindKeyValue,
				path:      path,
				lineStart: lineStart(data, from),
				lineEnd:   lineEnd(data, to),
				valueFrom: valueFrom,
				valueTo:   to,
			}
			doc.entries = append(doc.entries, e)
			if table >= 0 {
				doc.entries[table].blockEnd = e.lineEnd
			} else {
				doc.rootEnd = e.lineEnd
			}
		}
	}
	if err := p.Error(); err != nil {
		return nil, fmt.Errorf("parse TOML: %w", err)
	}
	return doc, nil
}

// SetString sets path to a string value, replacing the existing value in place
// when the key is already present and inserting the key otherwise. Missing
// parent tables are appended to the end of the document.
func (d *Document) SetString(path []string, value string) error {
	if len(path) == 0 {
		return errors.New("TOML edit requires a key path")
	}
	// The shape check runs first: a key inside an array of tables would
	// otherwise be matched and spliced as if it were a plain table member.
	if err := d.checkShape(path); err != nil {
		return err
	}
	text, err := EncodeString(value)
	if err != nil {
		return err
	}
	if i := d.findKey(path); i >= 0 {
		d.add(splice{from: d.entries[i].valueFrom, to: d.entries[i].valueTo, priority: priorityDefault, text: text})
		return nil
	}
	key, err := encodeKey(path[len(path)-1])
	if err != nil {
		return err
	}
	at, priority, err := d.insertPoint(path[:len(path)-1])
	if err != nil {
		return err
	}
	d.insertLine(at, key+" = "+text, priority)
	return nil
}

// SetStrings sets every kv inside the table at path, leaving keys the caller
// does not name untouched.
func (d *Document) SetStrings(path []string, kvs []KV) error {
	for _, kv := range kvs {
		full := make([]string, 0, len(path)+1)
		full = append(full, path...)
		full = append(full, kv.Key)
		if err := d.SetString(full, kv.Value); err != nil {
			return err
		}
	}
	return nil
}

// DeleteTree removes the table at path, everything declared under it, and any
// blank lines the removal would leave behind. Deleting an absent path is a
// no-op.
func (d *Document) DeleteTree(path []string) error {
	if len(path) == 0 {
		return errors.New("TOML delete requires a key path")
	}
	var ranges [][2]int
	for i := range d.entries {
		e := &d.entries[i]
		if e.dropped || !isPrefix(path, e.path) {
			continue
		}
		if e.kind == kindArrayTable {
			return fmt.Errorf("%w: %s is an array of tables", ErrUnsupportedShape, strings.Join(e.path, "."))
		}
		e.dropped = true
		if e.kind == kindKeyValue {
			ranges = append(ranges, [2]int{e.lineStart, e.lineEnd})
			continue
		}
		ranges = append(ranges, [2]int{e.lineStart, e.blockEnd})
	}
	for _, r := range mergeRanges(ranges) {
		d.add(splice{from: r[0], to: d.skipBlankLines(r[1]), priority: priorityDefault})
	}
	return nil
}

// ChildNames lists, in document order, the distinct names declared one level
// below prefix.
func (d *Document) ChildNames(prefix []string) []string {
	seen := map[string]bool{}
	var out []string
	for i := range d.entries {
		e := &d.entries[i]
		if e.dropped || len(e.path) <= len(prefix) || !isPrefix(prefix, e.path) {
			continue
		}
		name := e.path[len(prefix)]
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// Bytes applies every pending splice to the original document.
func (d *Document) Bytes() ([]byte, error) {
	items := make([]splice, len(d.splices))
	copy(items, d.splices)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].from != items[j].from {
			return items[i].from < items[j].from
		}
		if items[i].priority != items[j].priority {
			return items[i].priority < items[j].priority
		}
		return items[i].seq < items[j].seq
	})
	var out bytes.Buffer
	cursor := 0
	for _, s := range items {
		if s.from < cursor {
			return nil, fmt.Errorf("%w: overlapping TOML edits at offset %d", ErrUnsupportedShape, s.from)
		}
		out.Write(d.data[cursor:s.from])
		out.WriteString(s.text)
		cursor = s.to
	}
	out.Write(d.data[cursor:])
	return out.Bytes(), nil
}

func (d *Document) add(s splice) {
	s.seq = d.seq
	d.seq++
	d.splices = append(d.splices, s)
}

// insertLine schedules text as a line of its own at offset at.
func (d *Document) insertLine(at int, text string, priority int) {
	prefix := ""
	if at > 0 && at <= len(d.data) && d.data[at-1] != '\n' && !d.lineBreaks[at] {
		prefix = d.newline
		d.lineBreaks[at] = true
	}
	d.add(splice{from: at, to: at, priority: priority, text: prefix + text + d.newline})
}

func (d *Document) findKey(path []string) int {
	for i := range d.entries {
		e := &d.entries[i]
		if !e.dropped && e.kind == kindKeyValue && samePath(e.path, path) {
			return i
		}
	}
	return -1
}

func (d *Document) findTable(path []string) int {
	for i := range d.entries {
		e := &d.entries[i]
		if !e.dropped && e.kind == kindTable && samePath(e.path, path) {
			return i
		}
	}
	return -1
}

// checkShape rejects a target the document already declares as something other
// than a plain key inside a plain table.
func (d *Document) checkShape(path []string) error {
	for i := range d.entries {
		e := &d.entries[i]
		if e.dropped {
			continue
		}
		switch e.kind {
		case kindKeyValue:
			if len(e.path) < len(path) && isPrefix(e.path, path) {
				return fmt.Errorf("%w: %s holds a value, not a table", ErrUnsupportedShape, strings.Join(e.path, "."))
			}
		case kindArrayTable:
			if isPrefix(e.path, path) {
				return fmt.Errorf("%w: %s is an array of tables", ErrUnsupportedShape, strings.Join(e.path, "."))
			}
		case kindTable:
			if samePath(e.path, path) {
				return fmt.Errorf("%w: %s is a table, not a value", ErrUnsupportedShape, strings.Join(e.path, "."))
			}
		}
	}
	return nil
}

// insertPoint returns the offset a new key belonging to parent must be written
// at, creating parent's table header when it does not exist yet.
func (d *Document) insertPoint(parent []string) (int, int, error) {
	if len(parent) == 0 {
		return d.rootInsertPoint(), priorityRootKey, nil
	}
	if i := d.findTable(parent); i >= 0 {
		return d.entries[i].blockEnd, priorityDefault, nil
	}
	if d.hasImplicitTable(parent) {
		return 0, 0, fmt.Errorf("%w: %s is an implicit table", ErrUnsupportedShape, strings.Join(parent, "."))
	}
	at, err := d.createTable(parent)
	return at, priorityDefault, err
}

// hasImplicitTable reports a parent path created by a dotted key such as
// `model_providers.ai-gateway.name = ...`. Adding an explicit table header for
// that path would make the TOML invalid, so callers must use the semantic
// fallback instead of splicing a duplicate header.
func (d *Document) hasImplicitTable(path []string) bool {
	for i := range d.entries {
		e := &d.entries[i]
		if e.dropped || e.kind != kindKeyValue {
			continue
		}
		if len(e.path) > len(path) && isPrefix(path, e.path) {
			return true
		}
	}
	return false
}

// rootInsertPoint is the end of the root table's own key-value block. A root
// key written after the first table header would silently become a member of
// that table, so it must go above it.
func (d *Document) rootInsertPoint() int {
	if d.rootEnd > 0 {
		return d.rootEnd
	}
	if d.firstTable >= 0 {
		at := d.commentBlockStart(d.entries[d.firstTable].lineStart)
		// A root key pushed in front of the document's first table needs a blank
		// line after it, otherwise it reads as part of that table's block.
		if !d.rootTrailer {
			d.rootTrailer = true
			d.add(splice{from: at, to: at, priority: priorityRootTrailer, text: d.newline})
		}
		return at
	}
	return len(d.data)
}

// commentBlockStart walks back over the comment lines directly above at so an
// inserted key does not separate a comment from the table it documents.
func (d *Document) commentBlockStart(at int) int {
	for at > 0 {
		previous := lineStart(d.data, at-1)
		line := bytes.TrimSpace(d.data[previous:at])
		if len(line) == 0 || line[0] != '#' {
			return at
		}
		at = previous
	}
	return at
}

func (d *Document) createTable(path []string) (int, error) {
	if err := d.checkShape(path); err != nil {
		return 0, err
	}
	header, err := encodePath(path)
	if err != nil {
		return 0, err
	}
	at := len(d.data)
	separator := ""
	if len(d.data) > 0 && !bytes.HasSuffix(d.data, []byte(d.newline+d.newline)) {
		separator = d.newline
	}
	d.entries = append(d.entries, entry{
		kind:      kindTable,
		path:      append([]string(nil), path...),
		lineStart: at,
		lineEnd:   at,
		blockEnd:  at,
	})
	d.insertLine(at, separator+"["+header+"]", priorityDefault)
	return at, nil
}

func (d *Document) skipBlankLines(at int) int {
	for at < len(d.data) {
		end := lineEnd(d.data, at)
		if len(bytes.TrimSpace(d.data[at:end])) != 0 {
			return at
		}
		at = end
	}
	return at
}

// expressionKey returns the dotted key of a table header or key-value, the
// offset of its first key byte and the offset just past its last key byte.
func expressionKey(node *unstable.Node) ([]string, int, int, error) {
	var path []string
	first, last := -1, -1
	it := node.Key()
	for it.Next() {
		key := it.Node()
		if first < 0 {
			first = int(key.Raw.Offset)
		}
		last = int(key.Raw.Offset) + int(key.Raw.Length)
		path = append(path, string(key.Data))
	}
	if first < 0 {
		return nil, 0, 0, errors.New("TOML expression without a key")
	}
	return path, first, last, nil
}

// valueStart finds the first byte of a key-value's value. TOML forbids a
// newline between the key, the separator and the value, so only spaces and
// tabs can appear in between.
func valueStart(data []byte, keyEnd, valueEnd int) (int, error) {
	i := keyEnd
	for i < valueEnd && (data[i] == ' ' || data[i] == '\t') {
		i++
	}
	if i >= valueEnd || data[i] != '=' {
		return 0, fmt.Errorf("expected '=' after TOML key at offset %d", keyEnd)
	}
	i++
	for i < valueEnd && (data[i] == ' ' || data[i] == '\t') {
		i++
	}
	return i, nil
}

func lineStart(data []byte, offset int) int {
	for i := offset - 1; i >= 0; i-- {
		if data[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

func lineEnd(data []byte, offset int) int {
	for i := offset; i < len(data); i++ {
		if data[i] == '\n' {
			return i + 1
		}
	}
	return len(data)
}

func mergeRanges(ranges [][2]int) [][2]int {
	if len(ranges) < 2 {
		return ranges
	}
	sorted := make([][2]int, len(ranges))
	copy(sorted, ranges)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i][0] != sorted[j][0] {
			return sorted[i][0] < sorted[j][0]
		}
		return sorted[i][1] < sorted[j][1]
	})
	out := sorted[:1]
	for _, r := range sorted[1:] {
		last := &out[len(out)-1]
		if r[0] <= last[1] {
			if r[1] > last[1] {
				last[1] = r[1]
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func samePath(a, b []string) bool {
	return len(a) == len(b) && isPrefix(a, b)
}

func isPrefix(prefix, path []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i, part := range prefix {
		if path[i] != part {
			return false
		}
	}
	return true
}

// EncodeString renders v as a TOML string. Literal quoting is preferred so
// Windows paths stay readable, exactly as go-toml renders them.
func EncodeString(v string) (string, error) {
	if !utf8.ValidString(v) {
		return "", errors.New("TOML string value is not valid UTF-8")
	}
	if !strings.ContainsRune(v, '\'') && !hasControl(v) {
		return "'" + v + "'", nil
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}

func hasControl(v string) bool {
	for _, r := range v {
		if (r < 0x20 && r != '\t') || r == 0x7f {
			return true
		}
	}
	return false
}

func encodeKey(key string) (string, error) {
	if isBareKey(key) {
		return key, nil
	}
	return EncodeString(key)
}

func encodePath(path []string) (string, error) {
	parts := make([]string, 0, len(path))
	for _, part := range path {
		encoded, err := encodeKey(part)
		if err != nil {
			return "", err
		}
		parts = append(parts, encoded)
	}
	return strings.Join(parts, "."), nil
}

func isBareKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}
