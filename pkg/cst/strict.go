package cst

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
)

// Strict decoding is opt-in and layered on the lenient path, which never fails
// on malformed input: Parse builds a tree from any bytes, and the typed
// extractors coerce or skip a value of the wrong kind. Two checks close that
// gap without changing what the lenient path produces:
//
//   - Validate walks a parsed tree and reports the first place it is not
//     well-formed TOML (an unterminated string, a missing `=`, ...).
//   - Value.TypeErrors, run after a decoder has walked the model, reports the
//     values whose TOML type did not match the field that claimed them.
//
// A generated DecodeXStrict runs both around the ordinary decode.

// SyntaxError reports input that is not well-formed TOML. Line and Column are
// 1-based; Column counts bytes.
type SyntaxError struct {
	Line, Column int
	Msg          string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Msg)
}

// TypeError reports a value whose TOML type does not match the Go field its key
// decodes into. Key is the dotted path Undecoded would report. Line and Column
// locate the key (1-based, Column in bytes) and are zero when the value has no
// single source position, such as a table defined only by its sub-tables.
type TypeError struct {
	Key          string
	Line, Column int
	Msg          string
}

func (e *TypeError) Error() string {
	if e.Line == 0 {
		return fmt.Sprintf("key %q: %s", e.Key, e.Msg)
	}
	return fmt.Sprintf("line %d, column %d: key %q: %s", e.Line, e.Column, e.Key, e.Msg)
}

// ParseStrict parses input like Parse and then rejects it with a *SyntaxError
// unless it is well-formed TOML. The returned tree is the one Parse builds.
func ParseStrict(input []byte) (*Node, error) {
	root, err := Parse(input)
	if err != nil {
		return nil, err
	}
	if err := Validate(root); err != nil {
		return nil, err
	}
	return root, nil
}

// Validate reports the first place a parsed document is not well-formed TOML,
// as a *SyntaxError, or nil. It only inspects the tree.
//
// It checks structure: terminated strings, arrays, inline tables and table
// headers; a key, `=` and value in every key-value; one statement per line;
// separators between array and inline-table elements; and the spelling of keys,
// integers, floats and date-times. It does not check escape sequences or
// control characters inside strings. Duplicate keys are Decompose's job.
//
// It judges the tree the parser built, so it also rejects the few valid
// spellings the lexer misreads (#146): `inf`/`nan` inside an inline table, a
// dotted key leading an inline table, and whitespace before a key's dot.
func Validate(root *Node) error {
	v := &validator{positions: newPositions(root)}
	if err := v.statements(root.Children); err != nil {
		return err
	}
	return nil
}

type validator struct {
	positions *positions
	// unterminated is the last statement that did not end in a newline. That is
	// fine at the end of input and an error if anything follows on its line.
	unterminated *Node
}

func (v *validator) errAt(n *Node, format string, args ...any) *SyntaxError {
	line, column := v.positions.of(n)
	return &SyntaxError{Line: line, Column: column, Msg: fmt.Sprintf(format, args...)}
}

func (v *validator) statements(children []*Node) *SyntaxError {
	for _, c := range children {
		switch c.Kind {
		case NodeWhitespace, NodeNewline, NodeComment:
			continue
		case NodeKeyValue, NodeTable, NodeArrayTable:
		default:
			return v.errAt(c, "unexpected %q", c.Bytes())
		}
		if v.unterminated != nil {
			return v.errAt(c, "expected newline before %q", firstLine(c.Bytes()))
		}
		if c.Kind == NodeKeyValue {
			if err := v.keyValue(c); err != nil {
				return err
			}
			if last := c.Children[len(c.Children)-1]; last.Kind != NodeNewline {
				v.unterminated = c
			}
			continue
		}
		bodyStart, err := v.tableHeader(c)
		if err != nil {
			return err
		}
		if err := v.statements(c.Children[bodyStart:]); err != nil {
			return err
		}
	}
	return nil
}

// tableHeader validates the `[a.b]` / `[[a.b]]` line of a table node and returns
// the index of its first body child.
func (v *validator) tableHeader(t *Node) (int, *SyntaxError) {
	brackets := 1
	if t.Kind == NodeArrayTable {
		brackets = 2
	}
	i := brackets // the parser always emits the opening brackets
	keys, wantKey := 0, true
keyParts:
	for ; i < len(t.Children); i++ {
		c := t.Children[i]
		switch c.Kind {
		case NodeWhitespace:
		case NodeKey:
			if !wantKey || !isValidKey(c.Raw) {
				return 0, v.errAt(c, "invalid key %q in table header", c.Raw)
			}
			keys++
			wantKey = false
		case NodeDot:
			if wantKey {
				return 0, v.errAt(c, "unexpected '.' in table header")
			}
			wantKey = true
		default:
			break keyParts
		}
	}
	closes := 0
	for ; i < len(t.Children) && t.Children[i].Kind == NodeBracketClose; i++ {
		closes++
	}
	switch {
	case closes != brackets:
		return 0, v.errAt(t, "unterminated table header")
	case keys == 0:
		return 0, v.errAt(t, "empty table header")
	case wantKey:
		return 0, v.errAt(t, "table header ends with '.'")
	}
	for ; i < len(t.Children); i++ {
		switch c := t.Children[i]; c.Kind {
		case NodeWhitespace, NodeComment:
		case NodeNewline:
			return i + 1, nil
		default:
			return 0, v.errAt(c, "expected newline after table header")
		}
	}
	v.unterminated = t
	return i, nil
}

func (v *validator) keyValue(kv *Node) *SyntaxError {
	hasEquals := false
	for _, c := range kv.Children {
		switch c.Kind {
		case NodeKey:
			if !hasEquals && !isValidKey(c.Raw) {
				return v.errAt(c, "invalid key %q", c.Raw)
			}
		case NodeDottedKey:
			for _, part := range c.Children {
				if part.Kind == NodeKey && !isValidKey(part.Raw) {
					return v.errAt(part, "invalid key %q", part.Raw)
				}
			}
		case NodeEquals:
			hasEquals = true
		}
	}
	if !hasEquals {
		return v.errAt(kv, "expected '=' after key")
	}
	value := KeyValueValue(kv)
	if value == nil {
		return v.errAt(kv, "missing value")
	}
	return v.value(value)
}

func (v *validator) value(n *Node) *SyntaxError {
	switch n.Kind {
	case NodeString:
		// The parser stores any token it could not read as a value in a string
		// node, so this is also where a missing or bare-word value surfaces.
		if msg := stringFault(n.Raw); msg != "" {
			return v.errAt(n, "%s", msg)
		}
	case NodeInteger:
		if !integerPattern.Match(n.Raw) {
			return v.errAt(n, "invalid integer %q", n.Raw)
		}
	case NodeFloat:
		if !floatPattern.Match(n.Raw) {
			return v.errAt(n, "invalid float %q", n.Raw)
		}
	case NodeDateTime:
		if !dateTimePattern.Match(n.Raw) {
			return v.errAt(n, "invalid date-time %q", n.Raw)
		}
	case NodeBool:
	case NodeArray:
		return v.elements(n, NodeBracketClose, "array")
	case NodeInlineTable:
		return v.elements(n, NodeBraceClose, "inline table")
	default:
		return v.errAt(n, "invalid value %q", n.Bytes())
	}
	return nil
}

// elements validates the comma-separated body of an array or inline table.
func (v *validator) elements(n *Node, closeKind NodeKind, what string) *SyntaxError {
	last := len(n.Children) - 1
	if last < 1 || n.Children[last].Kind != closeKind {
		return v.errAt(n, "unterminated %s", what)
	}
	afterElement := false
	for _, c := range n.Children[1:last] {
		switch c.Kind {
		case NodeWhitespace, NodeNewline, NodeComment:
			continue
		case NodeComma:
			if !afterElement {
				return v.errAt(c, "unexpected ',' in %s", what)
			}
			afterElement = false
			continue
		}
		if afterElement {
			return v.errAt(c, "expected ',' between %s elements", what)
		}
		var err *SyntaxError
		switch {
		case closeKind == NodeBraceClose && c.Kind == NodeKeyValue:
			err = v.keyValue(c)
		case closeKind == NodeBraceClose:
			err = v.errAt(c, "unexpected %q in inline table", c.Bytes())
		default:
			err = v.value(c)
		}
		if err != nil {
			return err
		}
		afterElement = true
	}
	return nil
}

var (
	bareKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	integerPattern = regexp.MustCompile(`^(?:[+-]?(?:0|[1-9](?:_?[0-9])*)|0x[0-9A-Fa-f](?:_?[0-9A-Fa-f])*|0o[0-7](?:_?[0-7])*|0b[01](?:_?[01])*)$`)
	floatPattern   = regexp.MustCompile(`^[+-]?(?:inf|nan|(?:0|[1-9](?:_?[0-9])*)(?:\.[0-9](?:_?[0-9])*)?(?:[eE][+-]?[0-9](?:_?[0-9])*)?)$`)
	// The lexer ends an unquoted value at a space, so the space-separated
	// date-time spelling never reaches here as one token.
	dateTimePattern = regexp.MustCompile(`^(?:[0-9]{4}-[0-9]{2}-[0-9]{2}(?:[Tt][0-9]{2}:[0-9]{2}(?::[0-9]{2}(?:\.[0-9]+)?)?(?:[Zz]|[+-][0-9]{2}:[0-9]{2})?)?|[0-9]{2}:[0-9]{2}(?::[0-9]{2}(?:\.[0-9]+)?)?)$`)
)

func isValidKey(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case '"':
		return !bytes.HasPrefix(raw, []byte(`"""`)) && isClosedSingleLine(raw, '"', true)
	case '\'':
		return !bytes.HasPrefix(raw, []byte(`'''`)) && isClosedSingleLine(raw, '\'', false)
	}
	return bareKeyPattern.Match(raw)
}

// stringFault returns what is wrong with a string node's raw bytes, or "".
func stringFault(raw []byte) string {
	closed := false
	switch {
	case len(raw) == 0 || raw[0] == '\n' || raw[0] == '\r':
		return "missing value"
	case bytes.HasPrefix(raw, []byte(`"""`)):
		closed = isClosedMultiline(raw, '"', true)
	case bytes.HasPrefix(raw, []byte(`'''`)):
		closed = isClosedMultiline(raw, '\'', false)
	case raw[0] == '"':
		closed = isClosedSingleLine(raw, '"', true)
	case raw[0] == '\'':
		closed = isClosedSingleLine(raw, '\'', false)
	default:
		return fmt.Sprintf("invalid value %q", raw)
	}
	if !closed {
		return "unterminated string"
	}
	return ""
}

// isClosedSingleLine reports whether raw is a one-line string whose closing
// quote is its last byte. The lexer runs an unclosed string on across newlines
// to the next quote or the end of input, so a newline inside means unclosed.
func isClosedSingleLine(raw []byte, quote byte, escapes bool) bool {
	for i := 1; i < len(raw); i++ {
		switch c := raw[i]; {
		case c == '\n':
			return false
		case escapes && c == '\\':
			i++
		case c == quote:
			return i == len(raw)-1
		}
	}
	return false
}

// isClosedMultiline mirrors the lexer's scan of a `"""`/`”'` string: it ends at
// the first unescaped delimiter plus any quotes that directly follow it.
func isClosedMultiline(raw []byte, quote byte, escapes bool) bool {
	delimiter := []byte{quote, quote, quote}
	for i := 3; i < len(raw); i++ {
		if bytes.HasPrefix(raw[i:], delimiter) {
			for i += 3; i < len(raw) && raw[i] == quote; i++ {
			}
			return i == len(raw)
		}
		if escapes && raw[i] == '\\' {
			i++
		}
	}
	return false
}

func firstLine(b []byte) []byte {
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		return b[:i]
	}
	return b
}

// positions maps the nodes of one tree to line/column. Nodes carry no offsets,
// so they are recovered from the byte-exact concatenation of the leaves.
type positions struct {
	src    []byte
	starts map[*Node]int
}

func newPositions(root *Node) *positions {
	p := &positions{starts: make(map[*Node]int)}
	p.index(root)
	return p
}

func (p *positions) index(n *Node) {
	p.starts[n] = len(p.src)
	if len(n.Children) == 0 {
		p.src = append(p.src, n.Raw...)
		return
	}
	for _, c := range n.Children {
		p.index(c)
	}
}

func (p *positions) of(n *Node) (line, column int) {
	start, ok := p.starts[n]
	if !ok {
		return 0, 0
	}
	before := p.src[:start]
	line = 1 + bytes.Count(before, []byte("\n"))
	column = start - bytes.LastIndexByte(before, '\n')
	return line, column
}

// leafWant is the TOML value kind a decoder expected when it consumed a leaf
// through an extractor that does not check the kind itself.
type leafWant int

const (
	wantAny    leafWant = iota
	wantString          // a Go string
	wantText            // an encoding.TextUnmarshaler: a string or a date-time
)

// Field is Get for a decoder that owns key: it also records that key names a
// field of the type being decoded, whatever the value turns out to be. A known
// key whose value the decoder then leaves unread is a type mismatch; an unknown
// one is only undecoded. Generated decoders call it.
func (v *Value) Field(key string) (*Value, bool) {
	f, ok := v.Get(key)
	if ok {
		f.known = true
	}
	return f, ok
}

// MarkEntriesKnown records every entry of this table as a known key: a map
// field accepts any key, so an entry it could not read is a type mismatch
// rather than an unknown key. Generated decoders call it.
func (v *Value) MarkEntriesKnown() {
	for i := range v.Fields {
		v.Fields[i].Val.known = true
	}
}

// MarkConsumedString is MarkConsumed for a leaf read into a Go string through
// ExtractString, which accepts a value of any kind. Generated decoders call it.
func (v *Value) MarkConsumedString() {
	v.MarkConsumed()
	v.want = wantString
}

// MarkConsumedText is MarkConsumed for a leaf handed to an
// encoding.TextUnmarshaler through ExtractString. Generated decoders call it.
func (v *Value) MarkConsumedText() {
	v.MarkConsumed()
	v.want = wantText
}

// TypeErrors reports, after a decoder has walked this model, every value whose
// TOML type did not match the field that claimed its key: a leaf consumed as a
// string that is not a string, and a known key the decoder left unread (a
// string in a bool field, a scalar where a table belongs, a mistyped map
// entry). Each is a *TypeError; they are joined with errors.Join, and the result
// is nil when there are none. Keys no field claims are not type errors: they
// stay in Undecoded.
//
// It relies on the decoder having used Field, MarkEntriesKnown and
// MarkConsumedString/Text, so it reports nothing for a subtree decoded by code
// generated before those existed.
func (v *Value) TypeErrors() error {
	c := &typeErrorCollector{root: v.root}
	v.collectTypeErrors("", c)
	return errors.Join(c.errs...)
}

type typeErrorCollector struct {
	root      *Node
	positions *positions
	errs      []error
}

func (c *typeErrorCollector) add(key string, v *Value, msg string) {
	at := v.Node
	if v.Kind == VLeaf {
		at = v.Leaf
	}
	e := &TypeError{Key: key, Msg: msg}
	if at != nil && c.root != nil {
		if c.positions == nil {
			c.positions = newPositions(c.root)
		}
		e.Line, e.Column = c.positions.of(at)
	}
	c.errs = append(c.errs, e)
}

func (v *Value) collectTypeErrors(path string, c *typeErrorCollector) {
	switch v.Kind {
	case VTable:
		for i := range v.Fields {
			f := &v.Fields[i]
			f.Val.checkType(joinPath(path, []string{f.Key}), c)
		}
	case VArray:
		for i := range v.Items {
			v.Items[i].checkType(fmt.Sprintf("%s[%d]", path, i), c)
		}
	}
}

func (v *Value) checkType(path string, c *typeErrorCollector) {
	switch {
	case v.full:
		if !v.want.accepts(v) {
			c.add(path, v, fmt.Sprintf("expected string, got %s", v.kindName()))
		}
	case v.seen:
		v.collectTypeErrors(path, c)
	case v.known:
		c.add(path, v, fmt.Sprintf("got %s, which does not match the field's type", v.kindName()))
	}
}

func (w leafWant) accepts(v *Value) bool {
	if w == wantAny || v.Kind != VLeaf {
		return true
	}
	value := KeyValueValue(v.Leaf)
	if value == nil {
		return false
	}
	return value.Kind == NodeString || (w == wantText && value.Kind == NodeDateTime)
}

func (v *Value) kindName() string {
	switch v.Kind {
	case VTable:
		return "table"
	case VArray:
		return "array of tables"
	}
	value := KeyValueValue(v.Leaf)
	if value == nil {
		return "no value"
	}
	switch value.Kind {
	case NodeString:
		return "string"
	case NodeInteger:
		return "integer"
	case NodeFloat:
		return "float"
	case NodeBool:
		return "boolean"
	case NodeDateTime:
		return "date-time"
	case NodeArray:
		return "array"
	default:
		return "an unrecognized value"
	}
}
