package document

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/amarbel-llc/tommy/pkg/cst"
)

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

func (doc *Document) Bytes() []byte {
	return doc.root.Bytes()
}

// Get retrieves a value by dotted key path and converts it to the requested Go type.
func Get[T any](doc *Document, key string) (T, error) {
	var zero T

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

func findValueNode(root *cst.Node, key string) (*cst.Node, error) {
	parts := strings.Split(key, ".")

	if len(parts) == 1 {
		return findValueInContainer(root, parts[0])
	}

	tableName := strings.Join(parts[:len(parts)-1], ".")
	leafKey := parts[len(parts)-1]

	tableNode := findTableNode(root, tableName)
	if tableNode == nil {
		return nil, fmt.Errorf("key %q not found", key)
	}

	node, err := findValueInContainer(tableNode, leafKey)
	if err != nil {
		return nil, fmt.Errorf("key %q not found", key)
	}

	return node, nil
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
	return nil, fmt.Errorf("key %q not found", key)
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
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
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

func encodeValue(value any) ([]byte, cst.NodeKind, error) {
	switch v := value.(type) {
	case string:
		return []byte(`"` + escapeString(v) + `"`), cst.NodeString, nil
	case int:
		return []byte(strconv.Itoa(v)), cst.NodeInteger, nil
	case int64:
		return []byte(strconv.FormatInt(v, 10)), cst.NodeInteger, nil
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

	container.Children = append(container.Children, kv)
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
