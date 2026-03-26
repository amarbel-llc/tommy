package document

import (
	"strings"

	"github.com/amarbel-llc/tommy/pkg/cst"
)

// GetComment returns the comment line(s) immediately above the given key.
// For dotted keys like "server.port", it looks inside the [server] table.
// Returns empty string if no comment exists above the key.
func (doc *Document) GetComment(key string) string {
	container, leafKey := doc.resolveContainer(key)
	if container == nil {
		return ""
	}
	return getCommentAboveKey(container, leafKey)
}

// SetComment sets or replaces the comment line(s) immediately above the given key.
// The comment should include the "# " prefix. Multi-line comments should be
// newline-separated (e.g. "# line one\n# line two").
func (doc *Document) SetComment(key, comment string) {
	container, leafKey := doc.resolveContainer(key)
	if container == nil {
		return
	}
	setCommentAboveKey(container, leafKey, comment)
}

// GetInlineComment returns the inline comment on the same line as the given key.
// Returns empty string if no inline comment exists.
func (doc *Document) GetInlineComment(key string) string {
	container, leafKey := doc.resolveContainer(key)
	if container == nil {
		return ""
	}
	kv := findKeyValueNode(container, leafKey)
	if kv == nil {
		return ""
	}
	return getInlineCommentFromKV(kv)
}

// SetInlineComment sets or replaces the inline comment on the given key's line.
// The comment should include the "# " prefix.
func (doc *Document) SetInlineComment(key, comment string) {
	container, leafKey := doc.resolveContainer(key)
	if container == nil {
		return
	}
	kv := findKeyValueNode(container, leafKey)
	if kv == nil {
		return
	}
	setInlineCommentOnKV(kv, comment)
}

// resolveContainer splits a dotted key into the container node and leaf key name.
func (doc *Document) resolveContainer(key string) (*cst.Node, string) {
	parts := strings.Split(key, ".")
	if len(parts) == 1 {
		return doc.root, key
	}
	tableName := strings.Join(parts[:len(parts)-1], ".")
	leafKey := parts[len(parts)-1]
	tableNode := findTableNode(doc.root, tableName)
	if tableNode == nil {
		return nil, ""
	}
	return tableNode, leafKey
}

// findKeyValueNode finds the NodeKeyValue child with the given key name.
func findKeyValueNode(container *cst.Node, key string) *cst.Node {
	for _, child := range container.Children {
		if child.Kind == cst.NodeKeyValue && keyValueName(child) == key {
			return child
		}
	}
	return nil
}

// getCommentAboveKey collects comment lines immediately before the key-value node.
func getCommentAboveKey(container *cst.Node, key string) string {
	kvIdx := -1
	for i, child := range container.Children {
		if child.Kind == cst.NodeKeyValue && keyValueName(child) == key {
			kvIdx = i
			break
		}
	}
	if kvIdx < 0 {
		return ""
	}

	// Walk backwards from kvIdx, collecting comments.
	// Stop at anything that isn't a comment or whitespace/newline between comments.
	var commentLines []string
	for i := kvIdx - 1; i >= 0; i-- {
		child := container.Children[i]
		switch child.Kind {
		case cst.NodeComment:
			commentLines = append(commentLines, strings.TrimRight(string(child.Raw), "\r\n"))
		case cst.NodeNewline, cst.NodeWhitespace:
			continue
		default:
			goto done
		}
	}
done:
	// Reverse to get top-to-bottom order
	for i, j := 0, len(commentLines)-1; i < j; i, j = i+1, j-1 {
		commentLines[i], commentLines[j] = commentLines[j], commentLines[i]
	}
	return strings.Join(commentLines, "\n")
}

// setCommentAboveKey replaces or inserts comment lines above the key-value node.
func setCommentAboveKey(container *cst.Node, key, comment string) {
	kvIdx := -1
	for i, child := range container.Children {
		if child.Kind == cst.NodeKeyValue && keyValueName(child) == key {
			kvIdx = i
			break
		}
	}
	if kvIdx < 0 {
		return
	}

	// Find the range of existing comment/trivia nodes above the KV
	removeStart := kvIdx
	for i := kvIdx - 1; i >= 0; i-- {
		child := container.Children[i]
		switch child.Kind {
		case cst.NodeComment, cst.NodeNewline, cst.NodeWhitespace:
			removeStart = i
		default:
			goto foundStart
		}
	}
foundStart:

	// Build new comment nodes
	var newNodes []*cst.Node
	newNodes = append(newNodes, &cst.Node{Kind: cst.NodeComment, Raw: []byte(comment)})
	newNodes = append(newNodes, &cst.Node{Kind: cst.NodeNewline, Raw: []byte("\n")})

	// Replace: remove old trivia, insert new comment + newline
	tail := make([]*cst.Node, len(container.Children[kvIdx:]))
	copy(tail, container.Children[kvIdx:])
	container.Children = append(container.Children[:removeStart], append(newNodes, tail...)...)
}

// getInlineCommentFromKV extracts the inline comment from a key-value node's children.
func getInlineCommentFromKV(kv *cst.Node) string {
	for _, child := range kv.Children {
		if child.Kind == cst.NodeComment {
			return strings.TrimRight(string(child.Raw), "\r\n")
		}
	}
	return ""
}

// setInlineCommentOnKV sets or replaces the inline comment on a key-value node.
func setInlineCommentOnKV(kv *cst.Node, comment string) {
	// Find existing comment and replace it
	for i, child := range kv.Children {
		if child.Kind == cst.NodeComment {
			kv.Children[i] = &cst.Node{Kind: cst.NodeComment, Raw: []byte(comment)}
			return
		}
	}

	// No existing comment — insert space + comment before the trailing newline
	var insertIdx int
	for i := len(kv.Children) - 1; i >= 0; i-- {
		if kv.Children[i].Kind == cst.NodeNewline {
			insertIdx = i
			break
		}
	}

	spaceNode := &cst.Node{Kind: cst.NodeWhitespace, Raw: []byte(" ")}
	commentNode := &cst.Node{Kind: cst.NodeComment, Raw: []byte(comment)}

	tail := make([]*cst.Node, len(kv.Children[insertIdx:]))
	copy(tail, kv.Children[insertIdx:])
	kv.Children = append(kv.Children[:insertIdx], append([]*cst.Node{spaceNode, commentNode}, tail...)...)
}
