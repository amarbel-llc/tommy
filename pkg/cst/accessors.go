package cst

import (
	"fmt"
	"strconv"
	"strings"
)

// KeyValueName returns the key name from a NodeKeyValue node.
// For simple keys like `name = "value"`, returns "name".
// For dotted keys like `a.b.c = "value"`, returns "a.b.c".
func KeyValueName(kv *Node) string {
	for _, child := range kv.Children {
		if child.Kind == NodeKey {
			return StripQuotes(string(child.Raw))
		}
		if child.Kind == NodeDottedKey {
			var parts []string
			for _, sub := range child.Children {
				if sub.Kind == NodeKey {
					parts = append(parts, StripQuotes(string(sub.Raw)))
				}
			}
			return strings.Join(parts, ".")
		}
	}
	return ""
}

// KeyValueValue returns the value node from a NodeKeyValue node.
// The value is the first non-whitespace child after the NodeEquals.
func KeyValueValue(kv *Node) *Node {
	foundEquals := false
	for _, child := range kv.Children {
		if child.Kind == NodeEquals {
			foundEquals = true
			continue
		}
		if foundEquals && child.Kind != NodeWhitespace {
			return child
		}
	}
	return nil
}

// TableHeaderKey returns the dotted key from a NodeTable or NodeArrayTable
// header. For `[a.b.c]`, returns "a.b.c".
func TableHeaderKey(table *Node) string {
	var parts []string
	for _, child := range table.Children {
		if child.Kind == NodeKey {
			parts = append(parts, StripQuotes(string(child.Raw)))
		}
	}
	return strings.Join(parts, ".")
}

// --- Type-specific value extraction ---
// Each Extract* function finds the value node in a NodeKeyValue,
// then converts from the node's Raw bytes.

// ExtractString extracts a string value from a NodeKeyValue.
func ExtractString(kv *Node) (string, bool) {
	v := KeyValueValue(kv)
	if v == nil {
		return "", false
	}
	return StripQuotes(string(v.Raw)), true
}

// ExtractInt extracts an int value from a NodeKeyValue.
func ExtractInt(kv *Node) (int, bool) {
	v := KeyValueValue(kv)
	if v == nil || v.Kind != NodeInteger {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(string(v.Raw), "_", ""), 10, 64)
	if err != nil {
		return 0, false
	}
	return int(n), true
}

// ExtractInt64 extracts an int64 value from a NodeKeyValue.
func ExtractInt64(kv *Node) (int64, bool) {
	v := KeyValueValue(kv)
	if v == nil || v.Kind != NodeInteger {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(string(v.Raw), "_", ""), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ExtractUint64 extracts a uint64 value from a NodeKeyValue.
func ExtractUint64(kv *Node) (uint64, bool) {
	v := KeyValueValue(kv)
	if v == nil || v.Kind != NodeInteger {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.ReplaceAll(string(v.Raw), "_", ""), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ExtractFloat64 extracts a float64 value from a NodeKeyValue.
func ExtractFloat64(kv *Node) (float64, bool) {
	v := KeyValueValue(kv)
	if v == nil || v.Kind != NodeFloat {
		return 0, false
	}
	n, err := strconv.ParseFloat(string(v.Raw), 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ExtractBool extracts a bool value from a NodeKeyValue.
func ExtractBool(kv *Node) (bool, bool) {
	v := KeyValueValue(kv)
	if v == nil || v.Kind != NodeBool {
		return false, false
	}
	b, err := strconv.ParseBool(string(v.Raw))
	if err != nil {
		return false, false
	}
	return b, true
}

// ExtractStringSlice extracts a []string from a NodeKeyValue whose value is a NodeArray.
func ExtractStringSlice(kv *Node) ([]string, bool) {
	v := KeyValueValue(kv)
	if v == nil || v.Kind != NodeArray {
		return nil, false
	}
	var result []string
	for _, child := range v.Children {
		if child.Kind == NodeString {
			result = append(result, StripQuotes(string(child.Raw)))
		}
	}
	return result, true
}

// ExtractIntSlice extracts a []int from a NodeKeyValue whose value is a NodeArray.
func ExtractIntSlice(kv *Node) ([]int, bool) {
	v := KeyValueValue(kv)
	if v == nil || v.Kind != NodeArray {
		return nil, false
	}
	var result []int
	for _, child := range v.Children {
		if child.Kind == NodeInteger {
			n, err := strconv.ParseInt(strings.ReplaceAll(string(child.Raw), "_", ""), 10, 64)
			if err != nil {
				return nil, false
			}
			result = append(result, int(n))
		}
	}
	return result, true
}

// ExtractRaw extracts the value from a NodeKeyValue as a natural Go type
// (string, int64, float64, bool, or []any). Used for custom unmarshalers.
func ExtractRaw(kv *Node) (any, bool) {
	v := KeyValueValue(kv)
	if v == nil {
		return nil, false
	}
	switch v.Kind {
	case NodeString:
		return StripQuotes(string(v.Raw)), true
	case NodeInteger:
		n, err := strconv.ParseInt(strings.ReplaceAll(string(v.Raw), "_", ""), 10, 64)
		if err != nil {
			return nil, false
		}
		return n, true
	case NodeFloat:
		n, err := strconv.ParseFloat(string(v.Raw), 64)
		if err != nil {
			return nil, false
		}
		return n, true
	case NodeBool:
		b, err := strconv.ParseBool(string(v.Raw))
		if err != nil {
			return nil, false
		}
		return b, true
	case NodeArray:
		return extractArrayRaw(v), true
	default:
		return string(v.Raw), true
	}
}

func extractArrayRaw(arr *Node) []any {
	var result []any
	for _, child := range arr.Children {
		switch child.Kind {
		case NodeString:
			result = append(result, StripQuotes(string(child.Raw)))
		case NodeInteger:
			if n, err := strconv.ParseInt(strings.ReplaceAll(string(child.Raw), "_", ""), 10, 64); err == nil {
				result = append(result, n)
			}
		case NodeFloat:
			if n, err := strconv.ParseFloat(string(child.Raw), 64); err == nil {
				result = append(result, n)
			}
		case NodeBool:
			if b, err := strconv.ParseBool(string(child.Raw)); err == nil {
				result = append(result, b)
			}
		case NodeArray:
			result = append(result, extractArrayRaw(child))
		}
	}
	return result
}

// StripQuotes removes TOML quotes from a raw string value.
// Handles basic (""), literal (”), multiline ("""/”'), and bare strings.
func StripQuotes(s string) string {
	if len(s) >= 6 && s[:3] == `"""` && s[len(s)-3:] == `"""` {
		inner := s[3 : len(s)-3]
		if len(inner) > 0 && inner[0] == '\n' {
			inner = inner[1:]
		}
		return inner
	}
	if len(s) >= 6 && s[:3] == `'''` && s[len(s)-3:] == `'''` {
		inner := s[3 : len(s)-3]
		if len(inner) > 0 && inner[0] == '\n' {
			inner = inner[1:]
		}
		return inner
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return UnescapeString(s[1 : len(s)-1])
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}

// UnescapeString processes TOML escape sequences in a basic string.
func UnescapeString(s string) string {
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\t`, "\t")
	s = strings.ReplaceAll(s, `\r`, "\r")
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	return s
}

// FindArrayTableNodes returns all [[key]] array-table nodes from root children.
func FindArrayTableNodes(root *Node, key string) []*Node {
	var nodes []*Node
	for _, child := range root.Children {
		if child.Kind == NodeArrayTable && TableHeaderKey(child) == key {
			nodes = append(nodes, child)
		}
	}
	return nodes
}

// ExtractStringMap reads all key-value pairs from a container node (typically
// a NodeTable) as map[string]string.
func ExtractStringMap(container *Node) map[string]string {
	var m map[string]string
	for _, child := range container.Children {
		if child.Kind != NodeKeyValue {
			continue
		}
		if m == nil {
			m = make(map[string]string)
		}
		key := KeyValueName(child)
		if v, ok := ExtractString(child); ok {
			m[key] = v
		}
	}
	return m
}

// ==========================================================================
// Mutation accessors — used by generated encode code
// ==========================================================================

// --- Value encoding ---

// EncodeValue converts a Go value to TOML bytes and node kind.
func EncodeValue(value any) ([]byte, NodeKind, error) {
	switch v := value.(type) {
	case string:
		return []byte(`"` + EscapeString(v) + `"`), NodeString, nil
	case int:
		return []byte(strconv.Itoa(v)), NodeInteger, nil
	case int8:
		return []byte(strconv.FormatInt(int64(v), 10)), NodeInteger, nil
	case int16:
		return []byte(strconv.FormatInt(int64(v), 10)), NodeInteger, nil
	case int32:
		return []byte(strconv.FormatInt(int64(v), 10)), NodeInteger, nil
	case int64:
		return []byte(strconv.FormatInt(v, 10)), NodeInteger, nil
	case uint:
		return []byte(strconv.FormatUint(uint64(v), 10)), NodeInteger, nil
	case uint8:
		return []byte(strconv.FormatUint(uint64(v), 10)), NodeInteger, nil
	case uint16:
		return []byte(strconv.FormatUint(uint64(v), 10)), NodeInteger, nil
	case uint32:
		return []byte(strconv.FormatUint(uint64(v), 10)), NodeInteger, nil
	case uint64:
		return []byte(strconv.FormatUint(v, 10)), NodeInteger, nil
	case float32:
		return []byte(strconv.FormatFloat(float64(v), 'f', -1, 32)), NodeFloat, nil
	case float64:
		return []byte(strconv.FormatFloat(v, 'f', -1, 64)), NodeFloat, nil
	case bool:
		return []byte(strconv.FormatBool(v)), NodeBool, nil
	case []int:
		return encodeIntSliceBytes(v), NodeArray, nil
	case []string:
		return encodeStringSliceBytes(v), NodeArray, nil
	default:
		return nil, 0, fmt.Errorf("unsupported value type %T", value)
	}
}

// EncodeMultilineString encodes a string as TOML multiline basic string.
func EncodeMultilineString(value string) ([]byte, NodeKind) {
	return []byte(`"""` + "\n" + value + `"""`), NodeString
}

// EscapeString escapes special characters for a TOML basic string.
func EscapeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}

func encodeIntSliceBytes(v []int) []byte {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return []byte("[" + strings.Join(parts, ", ") + "]")
}

func encodeStringSliceBytes(v []string) []byte {
	parts := make([]string, len(v))
	for i, s := range v {
		parts[i] = `"` + EscapeString(s) + `"`
	}
	return []byte("[" + strings.Join(parts, ", ") + "]")
}

// --- Value mutations ---

// SetAny encodes a Go value and sets it as a key-value in the container.
func SetAny(container *Node, key string, value any) error {
	encoded, kind, err := EncodeValue(value)
	if err != nil {
		return err
	}
	return SetValue(container, key, encoded, kind)
}

// SetMultilineString sets a string value using TOML multiline basic string syntax.
func SetMultilineString(container *Node, key, value string) error {
	encoded, kind := EncodeMultilineString(value)
	return SetValue(container, key, encoded, kind)
}

// SetValue finds or creates a key-value in container and sets its value.
func SetValue(container *Node, key string, encoded []byte, kind NodeKind) error {
	for _, child := range container.Children {
		if child.Kind != NodeKeyValue {
			continue
		}
		if KeyValueName(child) == key {
			return replaceValue(child, encoded, kind)
		}
	}
	appendKeyValue(container, key, encoded, kind)
	return nil
}

// HasValue checks if a key-value exists in the container.
func HasValue(container *Node, key string) bool {
	for _, child := range container.Children {
		if child.Kind == NodeKeyValue && KeyValueName(child) == key {
			return true
		}
	}
	return false
}

// DeleteValue removes a key-value from the container.
func DeleteValue(container *Node, key string) {
	for i, child := range container.Children {
		if child.Kind == NodeKeyValue && KeyValueName(child) == key {
			container.Children = append(container.Children[:i], container.Children[i+1:]...)
			return
		}
	}
}

// DeleteAllValues removes all key-value children from a container,
// preserving table/array-table headers and other non-KV nodes.
func DeleteAllValues(container *Node) {
	var kept []*Node
	for _, child := range container.Children {
		if child.Kind == NodeKeyValue {
			continue
		}
		kept = append(kept, child)
	}
	container.Children = kept
}

func replaceValue(kv *Node, encoded []byte, kind NodeKind) error {
	foundEquals := false
	for i, child := range kv.Children {
		if child.Kind == NodeEquals {
			foundEquals = true
			continue
		}
		if foundEquals && child.Kind != NodeWhitespace {
			if kind == NodeArray {
				arrayDoc, _ := Parse([]byte("x = " + string(encoded) + "\n"))
				if arrayDoc != nil && len(arrayDoc.Children) > 0 {
					newVal := KeyValueValue(arrayDoc.Children[0])
					if newVal != nil {
						kv.Children[i] = newVal
					}
				}
			} else {
				kv.Children[i] = &Node{Kind: kind, Raw: encoded}
			}
			return nil
		}
	}
	return fmt.Errorf("malformed key-value node")
}

func appendKeyValue(container *Node, key string, encoded []byte, kind NodeKind) {
	var valueNode *Node
	if kind == NodeArray {
		arrayDoc, _ := Parse([]byte("x = " + string(encoded) + "\n"))
		if arrayDoc != nil && len(arrayDoc.Children) > 0 {
			valueNode = KeyValueValue(arrayDoc.Children[0])
		}
	}
	if valueNode == nil {
		valueNode = &Node{Kind: kind, Raw: encoded}
	}

	kv := &Node{
		Kind: NodeKeyValue,
		Children: []*Node{
			{Kind: NodeKey, Raw: []byte(key)},
			{Kind: NodeWhitespace, Raw: []byte(" ")},
			{Kind: NodeEquals, Raw: []byte("=")},
			{Kind: NodeWhitespace, Raw: []byte(" ")},
			valueNode,
			{Kind: NodeNewline, Raw: []byte("\n")},
		},
	}

	insertIdx := kvInsertIndex(container)
	newChildren := make([]*Node, 0, len(container.Children)+1)
	newChildren = append(newChildren, container.Children[:insertIdx]...)
	newChildren = append(newChildren, kv)
	newChildren = append(newChildren, container.Children[insertIdx:]...)
	container.Children = newChildren
}

// kvInsertIndex returns the index at which a new key-value should be inserted.
// Key-values go before any sub-table or array-table children.
func kvInsertIndex(container *Node) int {
	for i, child := range container.Children {
		if child.Kind == NodeTable || child.Kind == NodeArrayTable {
			for i > 0 && container.Children[i-1].Kind == NodeNewline {
				i--
			}
			return i
		}
	}
	return len(container.Children)
}

// --- Table creation with positional scoping ---

// EnsureChildTable finds or creates a [key] table as a child of parent.
// Unlike the document API's EnsureTable which always appends at root end,
// this inserts the new table immediately after parent in root.Children,
// fixing scoping for tables inside array-table entries.
func EnsureChildTable(root *Node, parent *Node, key string) *Node {
	fullKey := qualifiedKey(parent, key)

	// Search only within parent's scope (between parent and the next
	// same-level entry) to handle multiple [[array]] entries correctly.
	startIdx, endIdx := parentScope(root, parent)
	for i := startIdx; i < endIdx; i++ {
		child := root.Children[i]
		if child.Kind == NodeTable && TableHeaderKey(child) == fullKey {
			return child
		}
	}

	// Create new table node
	table := &Node{
		Kind: NodeTable,
		Children: []*Node{
			{Kind: NodeBracketOpen, Raw: []byte("[")},
			{Kind: NodeKey, Raw: []byte(fullKey)},
			{Kind: NodeBracketClose, Raw: []byte("]")},
			{Kind: NodeNewline, Raw: []byte("\n")},
		},
	}
	blankLine := &Node{Kind: NodeNewline, Raw: []byte("\n")}

	// Find position: insert after parent and its associated children
	insertIdx := childInsertIndex(root, parent)
	newChildren := make([]*Node, 0, len(root.Children)+2)
	newChildren = append(newChildren, root.Children[:insertIdx]...)
	newChildren = append(newChildren, blankLine, table)
	newChildren = append(newChildren, root.Children[insertIdx:]...)
	root.Children = newChildren
	return table
}

// EnsureChildSubTable finds or creates a [prefix.key] sub-table scoped to parent.
func EnsureChildSubTable(root *Node, parent *Node, prefix, key string) *Node {
	parentKey := TableHeaderKey(parent)
	var fullPrefix string
	if parentKey != "" {
		fullPrefix = parentKey + "." + prefix
	} else {
		fullPrefix = prefix
	}
	fullKey := fullPrefix + "." + key

	// Search within parent's scope
	startIdx, endIdx := parentScope(root, parent)
	for i := startIdx; i < endIdx; i++ {
		child := root.Children[i]
		if child.Kind == NodeTable && TableHeaderKey(child) == fullKey {
			return child
		}
	}

	// Create with dotted key structure
	table := &Node{
		Kind: NodeTable,
		Children: []*Node{
			{Kind: NodeBracketOpen, Raw: []byte("[")},
			{Kind: NodeKey, Raw: []byte(fullPrefix)},
			{Kind: NodeDot, Raw: []byte(".")},
			{Kind: NodeKey, Raw: []byte(key)},
			{Kind: NodeBracketClose, Raw: []byte("]")},
			{Kind: NodeNewline, Raw: []byte("\n")},
		},
	}
	blankLine := &Node{Kind: NodeNewline, Raw: []byte("\n")}

	insertIdx := childInsertIndex(root, parent)
	newChildren := make([]*Node, 0, len(root.Children)+2)
	newChildren = append(newChildren, root.Children[:insertIdx]...)
	newChildren = append(newChildren, blankLine, table)
	newChildren = append(newChildren, root.Children[insertIdx:]...)
	root.Children = newChildren
	return table
}

// AppendArrayTableEntryAfter appends a new [[key]] array-table entry,
// inserting it after the last existing [[key]] or at the end.
func AppendArrayTableEntryAfter(root *Node, key string) *Node {
	newNode := &Node{
		Kind: NodeArrayTable,
		Children: []*Node{
			{Kind: NodeBracketOpen, Raw: []byte("[")},
			{Kind: NodeBracketOpen, Raw: []byte("[")},
			{Kind: NodeKey, Raw: []byte(key)},
			{Kind: NodeBracketClose, Raw: []byte("]")},
			{Kind: NodeBracketClose, Raw: []byte("]")},
			{Kind: NodeNewline, Raw: []byte("\n")},
		},
	}

	lastIdx := -1
	for i, child := range root.Children {
		if child.Kind == NodeArrayTable && TableHeaderKey(child) == key {
			lastIdx = i
		}
	}

	blankLine := &Node{Kind: NodeNewline, Raw: []byte("\n")}
	if lastIdx >= 0 {
		// Insert after the last [[key]] and its associated children
		insertIdx := childInsertIndex(root, root.Children[lastIdx])
		newChildren := make([]*Node, 0, len(root.Children)+2)
		newChildren = append(newChildren, root.Children[:insertIdx]...)
		newChildren = append(newChildren, blankLine, newNode)
		newChildren = append(newChildren, root.Children[insertIdx:]...)
		root.Children = newChildren
	} else {
		root.Children = append(root.Children, blankLine, newNode)
	}
	return newNode
}

// qualifiedKey returns the full dotted key for a child of parent.
func qualifiedKey(parent *Node, key string) string {
	parentKey := TableHeaderKey(parent)
	if parentKey != "" {
		return parentKey + "." + key
	}
	return key
}

// parentScope returns the range [start, end) in root.Children that belongs to
// parent. For a [[servers]] entry, this is from the entry itself up to (but not
// including) the next [[servers]] entry or end of children.
// If parent is the root itself, returns [0, len(children)).
func parentScope(root *Node, parent *Node) (int, int) {
	if parent == root {
		return 0, len(root.Children)
	}
	parentIdx := -1
	for i, child := range root.Children {
		if child == parent {
			parentIdx = i
			break
		}
	}
	if parentIdx < 0 {
		return 0, len(root.Children)
	}

	parentKey := TableHeaderKey(parent)
	end := len(root.Children)
	for i := parentIdx + 1; i < len(root.Children); i++ {
		child := root.Children[i]
		// Stop at the next entry with the same header (next [[servers]] for [[servers]])
		if child.Kind == parent.Kind && TableHeaderKey(child) == parentKey {
			end = i
			break
		}
	}
	return parentIdx, end
}

// childInsertIndex finds the index in root.Children after parent and all its
// associated children (key-values, sub-tables that belong to parent).
// This enables positional insertion of new tables scoped to a specific parent.
func childInsertIndex(root *Node, parent *Node) int {
	parentIdx := -1
	for i, child := range root.Children {
		if child == parent {
			parentIdx = i
			break
		}
	}
	if parentIdx < 0 {
		return len(root.Children)
	}

	parentKey := TableHeaderKey(parent)
	prefix := parentKey + "."

	// Scan forward past all children that belong to this parent:
	// - NodeKeyValue (inline in parent)
	// - NodeNewline (whitespace)
	// - NodeTable whose header starts with parent's key
	// - NodeArrayTable whose header starts with parent's key
	// Stop at the next unrelated table/array-table or end.
	idx := parentIdx + 1
	for idx < len(root.Children) {
		child := root.Children[idx]
		switch child.Kind {
		case NodeNewline, NodeWhitespace, NodeComment:
			idx++
			continue
		case NodeTable:
			hdr := TableHeaderKey(child)
			if strings.HasPrefix(hdr, prefix) || hdr == parentKey {
				idx++
				continue
			}
			return idx
		case NodeArrayTable:
			hdr := TableHeaderKey(child)
			if strings.HasPrefix(hdr, prefix) {
				idx++
				continue
			}
			return idx
		default:
			idx++
			continue
		}
	}
	return idx
}
