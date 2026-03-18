package formatter

import (
	"bytes"
	"strings"

	"github.com/amarbel-llc/tommy/pkg/cst"
)

// Format parses TOML input to a CST, applies formatting rules, and returns
// the formatted output. On parse error, input is returned unchanged.
// Comments are preserved because they are CST nodes.
func Format(input []byte) []byte {
	root, err := cst.Parse(input)
	if err != nil {
		return input
	}

	normalizeEqualsWhitespace(root)
	normalizeInlineCommentSpacing(root)
	removeTrailingWhitespaceOnLines(root)

	// Serialize, apply line-based trailing-newline and blank-line normalization,
	// then return.
	out := root.Bytes()
	out = normalizeBlankLinesBetweenTables(out)
	out = normalizeTrailingNewlines(out)

	return out
}

// normalizeEqualsWhitespace ensures exactly one space before and after '='
// in every NodeKeyValue.
func normalizeEqualsWhitespace(node *cst.Node) {
	if node.Kind == cst.NodeKeyValue {
		normalizeEqualsInKeyValue(node)
	}

	for _, child := range node.Children {
		normalizeEqualsWhitespace(child)
	}
}

func normalizeEqualsInKeyValue(kv *cst.Node) {
	equalsIdx := -1
	for i, child := range kv.Children {
		if child.Kind == cst.NodeEquals {
			equalsIdx = i
			break
		}
	}

	if equalsIdx < 0 {
		return
	}

	singleSpace := &cst.Node{Kind: cst.NodeWhitespace, Raw: []byte(" ")}

	// Normalize whitespace before equals: remove all whitespace nodes
	// immediately before the equals, then insert exactly one space.
	var before []*cst.Node
	wsStart := equalsIdx
	for wsStart > 0 && kv.Children[wsStart-1].Kind == cst.NodeWhitespace {
		wsStart--
	}
	before = append(before, kv.Children[:wsStart]...)
	before = append(before, singleSpace)
	before = append(before, kv.Children[equalsIdx:]...)
	kv.Children = before

	// Recalculate equalsIdx after modification
	equalsIdx = -1
	for i, child := range kv.Children {
		if child.Kind == cst.NodeEquals {
			equalsIdx = i
			break
		}
	}

	// Normalize whitespace after equals: remove all whitespace nodes
	// immediately after the equals, then insert exactly one space.
	afterStart := equalsIdx + 1
	afterEnd := afterStart
	for afterEnd < len(kv.Children) && kv.Children[afterEnd].Kind == cst.NodeWhitespace {
		afterEnd++
	}

	var after []*cst.Node
	after = append(after, kv.Children[:equalsIdx+1]...)
	after = append(after, singleSpace)
	after = append(after, kv.Children[afterEnd:]...)
	kv.Children = after
}

// normalizeInlineCommentSpacing ensures exactly one space before a comment
// that follows a value on the same line (within a NodeKeyValue).
func normalizeInlineCommentSpacing(node *cst.Node) {
	if node.Kind == cst.NodeKeyValue {
		normalizeCommentInKeyValue(node)
	}
	for _, child := range node.Children {
		normalizeInlineCommentSpacing(child)
	}
}

func normalizeCommentInKeyValue(kv *cst.Node) {
	commentIdx := -1
	for i, child := range kv.Children {
		if child.Kind == cst.NodeComment {
			commentIdx = i
			break
		}
	}
	if commentIdx < 0 {
		return
	}

	// Remove all whitespace nodes immediately before the comment
	wsStart := commentIdx
	for wsStart > 0 && kv.Children[wsStart-1].Kind == cst.NodeWhitespace {
		wsStart--
	}

	singleSpace := &cst.Node{Kind: cst.NodeWhitespace, Raw: []byte(" ")}
	var result []*cst.Node
	result = append(result, kv.Children[:wsStart]...)
	result = append(result, singleSpace)
	result = append(result, kv.Children[commentIdx:]...)
	kv.Children = result
}

// removeTrailingWhitespaceOnLines removes trailing whitespace before newlines
// throughout the CST. This operates on the flat children of container nodes.
func removeTrailingWhitespaceOnLines(node *cst.Node) {
	if len(node.Children) > 0 {
		node.Children = trimTrailingWhitespaceBeforeNewlines(node.Children)
		for _, child := range node.Children {
			removeTrailingWhitespaceOnLines(child)
		}
	}
}

func trimTrailingWhitespaceBeforeNewlines(children []*cst.Node) []*cst.Node {
	var result []*cst.Node

	for i := 0; i < len(children); i++ {
		child := children[i]

		if child.Kind == cst.NodeWhitespace {
			// Look ahead: is the next non-whitespace sibling a newline or end-of-children?
			nextNonWS := i + 1
			for nextNonWS < len(children) && children[nextNonWS].Kind == cst.NodeWhitespace {
				nextNonWS++
			}
			if nextNonWS >= len(children) || children[nextNonWS].Kind == cst.NodeNewline {
				// Skip this whitespace (trailing on line)
				continue
			}
		}

		// For comment nodes, trim trailing whitespace from the raw text
		if child.Kind == cst.NodeComment {
			trimmed := strings.TrimRight(string(child.Raw), " \t")
			child.Raw = []byte(trimmed)
		}

		result = append(result, child)
	}

	return result
}

// normalizeBlankLinesBetweenTables works on serialized bytes. It ensures exactly
// one blank line before any line starting with '['.
func normalizeBlankLinesBetweenTables(data []byte) []byte {
	lines := splitLines(data)
	var result []string
	seenContent := false

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])

		if strings.HasPrefix(trimmed, "[") && seenContent {
			// Remove trailing blank lines from result
			for len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
				result = result[:len(result)-1]
			}
			// Add one blank line separator
			result = append(result, "")
			result = append(result, lines[i])
		} else {
			result = append(result, lines[i])
		}

		if trimmed != "" {
			seenContent = true
		}
	}

	return []byte(strings.Join(result, "\n"))
}

// normalizeTrailingNewlines ensures the output ends with exactly one newline.
func normalizeTrailingNewlines(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	data = bytes.TrimRight(data, "\n\r")
	if len(data) == 0 {
		return []byte("\n")
	}
	return append(data, '\n')
}

// splitLines splits data by '\n', preserving the structure but not the
// line terminators themselves.
func splitLines(data []byte) []string {
	s := string(data)
	// Remove trailing newline before split to avoid empty last element
	if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
	}
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
