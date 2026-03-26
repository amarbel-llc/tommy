package document

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/amarbel-llc/tommy/pkg/cst"
)

var ErrNotFound = fmt.Errorf("not found")

type Document struct {
	root *cst.Node
}

func Parse(input []byte) (*Document, error) {
	root, err := cst.Parse(input)
	if err != nil {
		return nil, err
	}
	return &Document{root: root}, nil
}

// ParseReader parses TOML from an io.Reader into a Document.
func ParseReader(r io.Reader) (*Document, error) {
	root, err := cst.ParseReader(r)
	if err != nil {
		return nil, err
	}
	return &Document{root: root}, nil
}

func (doc *Document) Root() *cst.Node {
	return doc.root
}

func (doc *Document) Bytes() []byte {
	return doc.root.Bytes()
}

type keySegment struct {
	key   string
	index int // -1 means append ([])
}

// parseKeyPath parses keys like "servers[0].name" into segments.
func parseKeyPath(key string) ([]keySegment, bool) {
	bracketIdx := strings.IndexByte(key, '[')
	if bracketIdx < 0 {
		return nil, false
	}

	var segments []keySegment
	first := key[:bracketIdx]
	rest := key[bracketIdx:]

	closeIdx := strings.IndexByte(rest, ']')
	if closeIdx < 0 {
		return nil, false
	}

	indexStr := rest[1:closeIdx]
	seg := keySegment{key: first}

	if indexStr == "" {
		seg.index = -1
	} else {
		idx, err := strconv.Atoi(indexStr)
		if err != nil {
			return nil, false
		}
		if idx < 0 {
			return nil, false
		}
		seg.index = idx
	}

	segments = append(segments, seg)

	remaining := rest[closeIdx+1:]
	if len(remaining) > 0 && remaining[0] != '.' {
		return nil, false
	}
	if len(remaining) > 0 && remaining[0] == '.' {
		remaining = remaining[1:]
		if remaining != "" {
			segments = append(segments, keySegment{key: remaining})
		}
	}

	return segments, true
}

// Get retrieves a value by dotted key path and converts it to the requested Go type.
func Get[T any](doc *Document, key string) (T, error) {
	var zero T

	if segments, ok := parseKeyPath(key); ok {
		if segments[0].index == -1 {
			return zero, fmt.Errorf("append syntax [] only valid in Set")
		}
		nodes := doc.FindArrayTableNodes(segments[0].key)
		if len(nodes) == 0 {
			return zero, fmt.Errorf("no array-of-tables entries for key %q", segments[0].key)
		}
		if segments[0].index >= len(nodes) {
			return zero, fmt.Errorf("index %d out of range (%d entries)", segments[0].index, len(nodes))
		}
		if len(segments) < 2 || segments[1].key == "" {
			return zero, fmt.Errorf("cannot Get entire array-table entry")
		}
		return GetFromContainer[T](doc, nodes[segments[0].index], segments[1].key)
	}

	valueNode, err := findValueNode(doc.root, key)
	if err != nil {
		return zero, err
	}

	result, err := convertNode[T](valueNode)
	if err != nil {
		return zero, fmt.Errorf("key %q: %w", key, err)
	}

	return result, nil
}

// Set updates or creates a key-value pair in the document.
func (doc *Document) Set(key string, value any) error {
	if segments, ok := parseKeyPath(key); ok {
		var container *cst.Node
		if segments[0].index == -1 {
			container = doc.AppendArrayTableEntry(segments[0].key)
		} else {
			nodes := doc.FindArrayTableNodes(segments[0].key)
			if len(nodes) == 0 {
				return fmt.Errorf("no array-of-tables entries for key %q", segments[0].key)
			}
			if segments[0].index >= len(nodes) {
				return fmt.Errorf("index %d out of range (%d entries)", segments[0].index, len(nodes))
			}
			container = nodes[segments[0].index]
		}
		if len(segments) < 2 || segments[1].key == "" {
			return fmt.Errorf("cannot Set entire array-table entry, specify a field")
		}
		return doc.SetInContainer(container, segments[1].key, value)
	}

	encoded, nodeKind, err := encodeValue(value)
	if err != nil {
		return err
	}

	parts := strings.Split(key, ".")

	if len(parts) == 1 {
		return setInContainer(doc.root, parts[0], encoded, nodeKind)
	}

	// For dotted keys like "storage.path", find the table first
	tableName := strings.Join(parts[:len(parts)-1], ".")
	leafKey := parts[len(parts)-1]

	tableNode := findTableNode(doc.root, tableName)
	if tableNode == nil {
		return fmt.Errorf("table %q not found", tableName)
	}

	return setInContainer(tableNode, leafKey, encoded, nodeKind)
}

// Delete removes a key-value pair from the document.
func (doc *Document) Delete(key string) error {
	if segments, ok := parseKeyPath(key); ok {
		if segments[0].index == -1 {
			return fmt.Errorf("append syntax [] only valid in Set")
		}
		nodes := doc.FindArrayTableNodes(segments[0].key)
		if len(nodes) == 0 {
			return fmt.Errorf("no array-of-tables entries for key %q", segments[0].key)
		}
		if segments[0].index >= len(nodes) {
			return fmt.Errorf("index %d out of range (%d entries)", segments[0].index, len(nodes))
		}
		if len(segments) > 1 && segments[1].key != "" {
			return deleteFromContainer(nodes[segments[0].index], segments[1].key)
		}
		return doc.RemoveArrayTableEntry(nodes[segments[0].index])
	}

	parts := strings.Split(key, ".")

	if len(parts) == 1 {
		return deleteFromContainer(doc.root, parts[0])
	}

	tableName := strings.Join(parts[:len(parts)-1], ".")
	leafKey := parts[len(parts)-1]

	tableNode := findTableNode(doc.root, tableName)
	if tableNode == nil {
		return fmt.Errorf("table %q not found", tableName)
	}

	return deleteFromContainer(tableNode, leafKey)
}

// GetFromContainer reads a value from a specific table or array-table node.
func GetFromContainer[T any](doc *Document, container *cst.Node, key string) (T, error) {
	var zero T
	valueNode, err := findValueInContainer(container, key)
	if err != nil {
		return zero, err
	}
	result, err := convertNode[T](valueNode)
	if err != nil {
		return zero, fmt.Errorf("key %q: %w", key, err)
	}
	return result, nil
}

// GetRawFromContainer reads a value from a container and returns it as its
// natural Go type (string, int64, float64, bool, or []any) without requiring
// a type parameter.
func GetRawFromContainer(doc *Document, container *cst.Node, key string) (any, error) {
	valueNode, err := findValueInContainer(container, key)
	if err != nil {
		return nil, err
	}
	return convertNodeToRaw(valueNode)
}

func convertNodeToRaw(node *cst.Node) (any, error) {
	switch node.Kind {
	case cst.NodeString:
		return stripQuotes(string(node.Raw)), nil
	case cst.NodeInteger:
		v, err := strconv.ParseInt(string(node.Raw), 10, 64)
		if err != nil {
			return nil, err
		}
		return v, nil
	case cst.NodeFloat:
		v, err := strconv.ParseFloat(string(node.Raw), 64)
		if err != nil {
			return nil, err
		}
		return v, nil
	case cst.NodeBool:
		v, err := strconv.ParseBool(string(node.Raw))
		if err != nil {
			return nil, err
		}
		return v, nil
	case cst.NodeArray:
		return convertArrayToRaw(node)
	default:
		return string(node.Raw), nil
	}
}

func convertArrayToRaw(node *cst.Node) ([]any, error) {
	var result []any
	for _, child := range node.Children {
		switch child.Kind {
		case cst.NodeString, cst.NodeInteger, cst.NodeFloat, cst.NodeBool:
			v, err := convertNodeToRaw(child)
			if err != nil {
				return nil, err
			}
			result = append(result, v)
		}
	}
	return result, nil
}

// Has returns true if the key exists in the document.
func (doc *Document) Has(key string) bool {
	_, err := findValueNode(doc.root, key)
	return err == nil
}

// HasInContainer returns true if the key exists within the given container node.
func (doc *Document) HasInContainer(container *cst.Node, key string) bool {
	_, err := findValueInContainer(container, key)
	return err == nil
}

// SetInContainer sets a key-value within a specific table or array-table node.
func (doc *Document) SetInContainer(container *cst.Node, key string, value any) error {
	encoded, nodeKind, err := encodeValue(value)
	if err != nil {
		return err
	}
	return setInContainer(container, key, encoded, nodeKind)
}

// SetMultiline sets a string value using multiline basic string syntax (""").
func (doc *Document) SetMultiline(key string, value string) error {
	encoded := []byte(`"""` + "\n" + value + `"""`)

	parts := strings.Split(key, ".")
	if len(parts) == 1 {
		return setInContainer(doc.root, parts[0], encoded, cst.NodeString)
	}

	tableName := strings.Join(parts[:len(parts)-1], ".")
	leafKey := parts[len(parts)-1]
	tableNode := findTableNode(doc.root, tableName)
	if tableNode == nil {
		return fmt.Errorf("table %q not found", tableName)
	}
	return setInContainer(tableNode, leafKey, encoded, cst.NodeString)
}

// SetMultilineInContainer sets a string value using multiline basic string syntax (""").
func (doc *Document) SetMultilineInContainer(container *cst.Node, key string, value string) error {
	encoded := []byte(`"""` + "\n" + value + `"""`)
	return setInContainer(container, key, encoded, cst.NodeString)
}

// DeleteFromContainer removes a key-value pair from a container node.
func (doc *Document) DeleteFromContainer(container *cst.Node, key string) error {
	return deleteFromContainer(container, key)
}

func findValueNode(root *cst.Node, key string) (*cst.Node, error) {
	parts := strings.Split(key, ".")

	if len(parts) == 1 {
		return findValueInContainer(root, parts[0])
	}

	tableName := strings.Join(parts[:len(parts)-1], ".")
	leafKey := parts[len(parts)-1]

	tableNode := findTableNode(root, tableName)
	if tableNode == nil {
		return nil, fmt.Errorf("key %q: %w", key, ErrNotFound)
	}

	node, err := findValueInContainer(tableNode, leafKey)
	if err != nil {
		return nil, fmt.Errorf("key %q: %w", key, ErrNotFound)
	}

	return node, nil
}

// FindTable returns the [name] table node from the document root, or nil.
func (doc *Document) FindTable(name string) *cst.Node {
	return findTableNode(doc.root, name)
}

func findTableNode(root *cst.Node, name string) *cst.Node {
	for _, child := range root.Children {
		if child.Kind != cst.NodeTable {
			continue
		}
		if tableHeaderKey(child) == name {
			return child
		}
	}
	return nil
}

// FindArrayTableNodes returns all [[key]] CST nodes in document order.
// Returns nil if none exist.
func (doc *Document) FindArrayTableNodes(key string) []*cst.Node {
	var nodes []*cst.Node
	for _, child := range doc.root.Children {
		if child.Kind != cst.NodeArrayTable {
			continue
		}
		if tableHeaderKey(child) == key {
			nodes = append(nodes, child)
		}
	}
	return nodes
}

// FindNestedArrayTableNodes returns [[parentKey.childKey]] entries that belong
// to the parentIndex-th [[parentKey]] entry. In TOML, nested array-of-tables
// entries belong to the most recently defined parent entry. This method finds
// all [[parentKey.childKey]] nodes between the parentIndex-th and (parentIndex+1)-th
// [[parentKey]] nodes in document order.
func (doc *Document) FindNestedArrayTableNodes(parentKey string, parentIndex int, childKey string) []*cst.Node {
	fullKey := parentKey + "." + childKey
	parentCount := 0
	var nodes []*cst.Node
	inScope := false

	for _, child := range doc.root.Children {
		if child.Kind == cst.NodeArrayTable {
			header := tableHeaderKey(child)
			if header == parentKey {
				if parentCount == parentIndex {
					inScope = true
				} else if parentCount > parentIndex {
					break // past our parent entry
				}
				parentCount++
				continue
			}
			if inScope && header == fullKey {
				nodes = append(nodes, child)
			}
		}
	}

	return nodes
}

func tableHeaderKey(table *cst.Node) string {
	var parts []string
	for _, child := range table.Children {
		if child.Kind == cst.NodeKey {
			parts = append(parts, stripQuotes(string(child.Raw)))
		}
	}
	return strings.Join(parts, ".")
}

func findValueInContainer(container *cst.Node, key string) (*cst.Node, error) {
	for _, child := range container.Children {
		if child.Kind != cst.NodeKeyValue {
			continue
		}
		if keyValueName(child) == key {
			return keyValueValueNode(child), nil
		}
	}
	return nil, fmt.Errorf("key %q: %w", key, ErrNotFound)
}

func keyValueName(kv *cst.Node) string {
	for _, child := range kv.Children {
		if child.Kind == cst.NodeKey {
			return stripQuotes(string(child.Raw))
		}
		if child.Kind == cst.NodeDottedKey {
			var parts []string
			for _, sub := range child.Children {
				if sub.Kind == cst.NodeKey {
					parts = append(parts, stripQuotes(string(sub.Raw)))
				}
			}
			return strings.Join(parts, ".")
		}
	}
	return ""
}

func keyValueValueNode(kv *cst.Node) *cst.Node {
	foundEquals := false
	for _, child := range kv.Children {
		if child.Kind == cst.NodeEquals {
			foundEquals = true
			continue
		}
		if foundEquals && child.Kind != cst.NodeWhitespace {
			return child
		}
	}
	return nil
}

func stripQuotes(s string) string {
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
		return unescapeString(s[1 : len(s)-1])
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}

func unescapeString(s string) string {
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\t`, "\t")
	s = strings.ReplaceAll(s, `\r`, "\r")
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	return s
}

func convertNode[T any](node *cst.Node) (T, error) {
	var zero T
	var result any
	var err error

	switch any(zero).(type) {
	case string:
		result = stripQuotes(string(node.Raw))
	case int:
		var v int64
		v, err = strconv.ParseInt(string(node.Raw), 10, 64)
		result = int(v)
	case int64:
		result, err = strconv.ParseInt(string(node.Raw), 10, 64)
	case uint64:
		result, err = strconv.ParseUint(string(node.Raw), 10, 64)
	case float64:
		result, err = strconv.ParseFloat(string(node.Raw), 64)
	case bool:
		result, err = strconv.ParseBool(string(node.Raw))
	case []int:
		result, err = convertIntArray(node)
	case []string:
		result, err = convertStringArray(node)
	default:
		return zero, fmt.Errorf("unsupported type %T", zero)
	}

	if err != nil {
		return zero, err
	}

	return result.(T), nil
}

func convertIntArray(node *cst.Node) ([]int, error) {
	if node.Kind != cst.NodeArray {
		return nil, fmt.Errorf("expected array, got %v", node.Kind)
	}

	var result []int
	for _, child := range node.Children {
		if child.Kind == cst.NodeInteger {
			v, err := strconv.ParseInt(string(child.Raw), 10, 64)
			if err != nil {
				return nil, err
			}
			result = append(result, int(v))
		}
	}
	return result, nil
}

func convertStringArray(node *cst.Node) ([]string, error) {
	if node.Kind != cst.NodeArray {
		return nil, fmt.Errorf("expected array, got %v", node.Kind)
	}

	var result []string
	for _, child := range node.Children {
		if child.Kind == cst.NodeString {
			result = append(result, stripQuotes(string(child.Raw)))
		}
	}
	return result, nil
}

// IsMultilineString reports whether the value node for the given key uses
// multiline string syntax (""" or ''').
func (doc *Document) IsMultilineString(key string) bool {
	node, err := findValueNode(doc.root, key)
	if err != nil {
		return false
	}
	return isMultilineStringNode(node)
}

// IsMultilineStringInContainer is like IsMultilineString but searches within
// a specific container node.
func IsMultilineStringInContainer(container *cst.Node, key string) bool {
	node, err := findValueInContainer(container, key)
	if err != nil {
		return false
	}
	return isMultilineStringNode(node)
}

func isMultilineStringNode(node *cst.Node) bool {
	if node.Kind != cst.NodeString || len(node.Raw) < 6 {
		return false
	}
	s := string(node.Raw)
	return (s[:3] == `"""` && s[len(s)-3:] == `"""`) ||
		(s[:3] == `'''` && s[len(s)-3:] == `'''`)
}

func encodeValue(value any) ([]byte, cst.NodeKind, error) {
	switch v := value.(type) {
	case string:
		return []byte(`"` + escapeString(v) + `"`), cst.NodeString, nil
	case int:
		return []byte(strconv.Itoa(v)), cst.NodeInteger, nil
	case int64:
		return []byte(strconv.FormatInt(v, 10)), cst.NodeInteger, nil
	case uint64:
		return []byte(strconv.FormatUint(v, 10)), cst.NodeInteger, nil
	case float64:
		s := strconv.FormatFloat(v, 'f', -1, 64)
		return []byte(s), cst.NodeFloat, nil
	case bool:
		return []byte(strconv.FormatBool(v)), cst.NodeBool, nil
	case []int:
		return encodeIntSlice(v), cst.NodeArray, nil
	case []string:
		return encodeStringSlice(v), cst.NodeArray, nil
	default:
		return nil, 0, fmt.Errorf("unsupported value type %T", value)
	}
}

func escapeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}

func encodeIntSlice(v []int) []byte {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return []byte("[" + strings.Join(parts, ", ") + "]")
}

func encodeStringSlice(v []string) []byte {
	parts := make([]string, len(v))
	for i, s := range v {
		parts[i] = `"` + escapeString(s) + `"`
	}
	return []byte("[" + strings.Join(parts, ", ") + "]")
}

func setInContainer(container *cst.Node, key string, encoded []byte, kind cst.NodeKind) error {
	for _, child := range container.Children {
		if child.Kind != cst.NodeKeyValue {
			continue
		}
		if keyValueName(child) == key {
			return replaceValueInKeyValue(child, encoded, kind)
		}
	}

	// Key not found — append new key-value
	appendKeyValue(container, key, encoded, kind)
	return nil
}

func replaceValueInKeyValue(kv *cst.Node, encoded []byte, kind cst.NodeKind) error {
	foundEquals := false
	for i, child := range kv.Children {
		if child.Kind == cst.NodeEquals {
			foundEquals = true
			continue
		}
		if foundEquals && child.Kind != cst.NodeWhitespace {
			if kind == cst.NodeArray {
				// Replace with a freshly parsed array node
				arrayDoc, _ := cst.Parse([]byte("x = " + string(encoded) + "\n"))
				newVal := keyValueValueNode(arrayDoc.Children[0])
				kv.Children[i] = newVal
			} else {
				kv.Children[i] = &cst.Node{Kind: kind, Raw: encoded}
			}
			return nil
		}
	}
	return fmt.Errorf("malformed key-value node")
}

func appendKeyValue(container *cst.Node, key string, encoded []byte, kind cst.NodeKind) {
	var valueNode *cst.Node
	if kind == cst.NodeArray {
		arrayDoc, _ := cst.Parse([]byte("x = " + string(encoded) + "\n"))
		valueNode = keyValueValueNode(arrayDoc.Children[0])
	} else {
		valueNode = &cst.Node{Kind: kind, Raw: encoded}
	}

	kv := &cst.Node{
		Kind: cst.NodeKeyValue,
		Children: []*cst.Node{
			{Kind: cst.NodeKey, Raw: []byte(key)},
			{Kind: cst.NodeWhitespace, Raw: []byte(" ")},
			{Kind: cst.NodeEquals, Raw: []byte("=")},
			{Kind: cst.NodeWhitespace, Raw: []byte(" ")},
			valueNode,
			{Kind: cst.NodeNewline, Raw: []byte("\n")},
		},
	}

	insertIdx := bodyTrailingTriviaStart(container)

	newChildren := make([]*cst.Node, 0, len(container.Children)+1)
	newChildren = append(newChildren, container.Children[:insertIdx]...)
	newChildren = append(newChildren, kv)
	newChildren = append(newChildren, container.Children[insertIdx:]...)
	container.Children = newChildren
}

// bodyTrailingTriviaStart returns the index where trailing trivia begins in a
// container's children. New key-value entries should be inserted at this index
// so that blank-line separators between table sections remain at the end.
func bodyTrailingTriviaStart(container *cst.Node) int {
	n := len(container.Children)

	// Walk backwards past trailing NodeNewline children (blank-line separators).
	trailingStart := n
	for trailingStart > 0 && container.Children[trailingStart-1].Kind == cst.NodeNewline {
		trailingStart--
	}

	// For table/array-table nodes, the header ends with a newline that must not
	// be treated as trailing trivia. Find the header boundary (first newline
	// after the last bracket-close) and clamp.
	if container.Kind == cst.NodeTable || container.Kind == cst.NodeArrayTable {
		headerEnd := 0
		foundClose := false
		for i, child := range container.Children {
			if child.Kind == cst.NodeBracketClose {
				foundClose = true
			}
			if foundClose && child.Kind == cst.NodeNewline {
				headerEnd = i + 1
				break
			}
		}
		if trailingStart < headerEnd {
			trailingStart = headerEnd
		}
	}

	return trailingStart
}

// AppendArrayTableEntry adds a new [[key]] section after the last existing
// one, or at the end of the document. Returns the new node.
func (doc *Document) AppendArrayTableEntry(key string) *cst.Node {
	newNode := &cst.Node{
		Kind: cst.NodeArrayTable,
		Children: []*cst.Node{
			{Kind: cst.NodeBracketOpen, Raw: []byte("[")},
			{Kind: cst.NodeBracketOpen, Raw: []byte("[")},
			{Kind: cst.NodeKey, Raw: []byte(key)},
			{Kind: cst.NodeBracketClose, Raw: []byte("]")},
			{Kind: cst.NodeBracketClose, Raw: []byte("]")},
			{Kind: cst.NodeNewline, Raw: []byte("\n")},
		},
	}

	// Find the last [[key]] node to insert after it
	lastIdx := -1
	for i, child := range doc.root.Children {
		if child.Kind == cst.NodeArrayTable && tableHeaderKey(child) == key {
			lastIdx = i
		}
	}

	blankLine := &cst.Node{Kind: cst.NodeNewline, Raw: []byte("\n")}

	if lastIdx >= 0 {
		// Insert after the last entry
		insertIdx := lastIdx + 1
		newChildren := make([]*cst.Node, 0, len(doc.root.Children)+2)
		newChildren = append(newChildren, doc.root.Children[:insertIdx]...)
		newChildren = append(newChildren, blankLine, newNode)
		newChildren = append(newChildren, doc.root.Children[insertIdx:]...)
		doc.root.Children = newChildren
	} else {
		// No existing entries — append at end
		doc.root.Children = append(doc.root.Children, blankLine, newNode)
	}

	return newNode
}

// RemoveArrayTableEntry removes a [[key]] section and its body from the document.
func (doc *Document) RemoveArrayTableEntry(node *cst.Node) error {
	startIdx := -1
	for i, child := range doc.root.Children {
		if child == node {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return fmt.Errorf("node not found in document")
	}

	// The array-table node contains its key-value body as children,
	// so removing it removes the entire section.
	endIdx := startIdx + 1

	// Remove a preceding blank-line node if present
	removeFrom := startIdx
	if removeFrom > 0 && doc.root.Children[removeFrom-1].Kind == cst.NodeNewline {
		removeFrom--
	}

	doc.root.Children = append(doc.root.Children[:removeFrom], doc.root.Children[endIdx:]...)
	return nil
}

// FindTableInContainer finds a [container.key] table that belongs to the
// given container node. It first checks direct children, then searches the
// document root for qualified table headers.
func (doc *Document) FindTableInContainer(container *cst.Node, key string) *cst.Node {
	for _, child := range container.Children {
		if child.Kind == cst.NodeTable && tableHeaderKey(child) == key {
			return child
		}
	}

	containerKey := tableHeaderKey(container)
	if containerKey == "" {
		return nil
	}
	qualifiedKey := containerKey + "." + key
	for _, child := range doc.root.Children {
		if child.Kind == cst.NodeTable && tableHeaderKey(child) == qualifiedKey {
			return child
		}
	}
	return nil
}

// GetStringMapFromTable reads all key-value pairs from a [table] node as a
// map[string]string. Returns nil if the table has no key-value children.
func GetStringMapFromTable(table *cst.Node) map[string]string {
	var m map[string]string
	for _, child := range table.Children {
		if child.Kind != cst.NodeKeyValue {
			continue
		}
		key := keyValueName(child)
		valNode := keyValueValueNode(child)
		if valNode == nil {
			continue
		}
		if m == nil {
			m = make(map[string]string)
		}
		m[key] = stripQuotes(string(valNode.Raw))
	}
	return m
}

// EnsureTable finds or creates a [name] table section in the document.
// Returns the table node.
func (doc *Document) EnsureTable(name string) *cst.Node {
	if existing := findTableNode(doc.root, name); existing != nil {
		return existing
	}
	table := &cst.Node{
		Kind: cst.NodeTable,
		Children: []*cst.Node{
			{Kind: cst.NodeBracketOpen, Raw: []byte("[")},
			{Kind: cst.NodeKey, Raw: []byte(name)},
			{Kind: cst.NodeBracketClose, Raw: []byte("]")},
			{Kind: cst.NodeNewline, Raw: []byte("\n")},
		},
	}
	blankLine := &cst.Node{Kind: cst.NodeNewline, Raw: []byte("\n")}
	doc.root.Children = append(doc.root.Children, blankLine, table)
	return table
}

// FindSubTables returns all [prefix.key] table nodes for a given prefix,
// along with the sub-key (the part after the prefix dot).
// For example, FindSubTables("actions") returns tables like [actions.build].
func (doc *Document) FindSubTables(prefix string) []*cst.Node {
	var nodes []*cst.Node
	dotPrefix := prefix + "."
	for _, child := range doc.root.Children {
		if child.Kind != cst.NodeTable {
			continue
		}
		header := tableHeaderKey(child)
		if strings.HasPrefix(header, dotPrefix) {
			nodes = append(nodes, child)
		}
	}
	return nodes
}

// SubTableKey returns the sub-key portion of a [prefix.key] table header.
// For a table with header "actions.build", SubTableKey("actions") returns "build".
func SubTableKey(table *cst.Node, prefix string) string {
	header := tableHeaderKey(table)
	return strings.TrimPrefix(header, prefix+".")
}

// EnsureSubTable finds or creates a [prefix.key] table section.
func (doc *Document) EnsureSubTable(prefix, key string) *cst.Node {
	fullName := prefix + "." + key
	if existing := findTableNode(doc.root, fullName); existing != nil {
		return existing
	}
	table := &cst.Node{
		Kind: cst.NodeTable,
		Children: []*cst.Node{
			{Kind: cst.NodeBracketOpen, Raw: []byte("[")},
			{Kind: cst.NodeKey, Raw: []byte(prefix)},
			{Kind: cst.NodeDot, Raw: []byte(".")},
			{Kind: cst.NodeKey, Raw: []byte(key)},
			{Kind: cst.NodeBracketClose, Raw: []byte("]")},
			{Kind: cst.NodeNewline, Raw: []byte("\n")},
		},
	}
	blankLine := &cst.Node{Kind: cst.NodeNewline, Raw: []byte("\n")}
	doc.root.Children = append(doc.root.Children, blankLine, table)
	return table
}

// EnsureTableInContainer finds or creates a table for the given key within a
// container. It first checks direct children, then the document root for
// qualified headers (like FindTableInContainer), creating the table if absent.
func (doc *Document) EnsureTableInContainer(container *cst.Node, key string) *cst.Node {
	if existing := doc.FindTableInContainer(container, key); existing != nil {
		return existing
	}

	containerKey := tableHeaderKey(container)
	var fullName string
	if containerKey != "" {
		fullName = containerKey + "." + key
	} else {
		fullName = key
	}
	return doc.EnsureTable(fullName)
}

// DeleteAllInContainer removes all key-value children from a container node.
func DeleteAllInContainer(container *cst.Node) {
	var kept []*cst.Node
	for _, child := range container.Children {
		if child.Kind == cst.NodeKeyValue {
			continue
		}
		kept = append(kept, child)
	}
	container.Children = kept
}

// UndecodedKeys walks the CST and returns all key paths not present in
// the consumed set. Table headers are prefixed to their children
// (e.g. "hooks.create"). Keys under consumed tables are skipped entirely.
func UndecodedKeys(root *cst.Node, consumed map[string]bool) []string {
	var result []string
	for _, child := range root.Children {
		switch child.Kind {
		case cst.NodeKeyValue:
			key := keyValueName(child)
			if !consumed[key] {
				result = append(result, key)
			}
		case cst.NodeTable:
			tableName := tableHeaderKey(child)
			if consumed[tableName] {
				// Table was consumed (e.g. map field) — check inner keys
				for _, inner := range child.Children {
					if inner.Kind != cst.NodeKeyValue {
						continue
					}
					qualifiedKey := tableName + "." + keyValueName(inner)
					if !consumed[qualifiedKey] {
						result = append(result, qualifiedKey)
					}
				}
			} else {
				// Table itself is unknown
				result = append(result, tableName)
			}
		}
	}
	return result
}

// MarkAllConsumed marks all key-value children in a table as consumed,
// using the given prefix (e.g. "env") to build qualified keys like "env.FOO".
func MarkAllConsumed(table *cst.Node, prefix string, consumed map[string]bool) {
	for _, child := range table.Children {
		if child.Kind != cst.NodeKeyValue {
			continue
		}
		consumed[prefix+"."+keyValueName(child)] = true
	}
}

func deleteFromContainer(container *cst.Node, key string) error {
	for i, child := range container.Children {
		if child.Kind != cst.NodeKeyValue {
			continue
		}
		if keyValueName(child) == key {
			container.Children = append(container.Children[:i], container.Children[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("key %q not found", key)
}
