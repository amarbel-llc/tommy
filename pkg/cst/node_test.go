package cst

import "testing"

func TestNodeBytesReturnsRawForLeaf(t *testing.T) {
	node := &Node{
		Kind: NodeComment,
		Raw:  []byte("# hello"),
	}
	got := string(node.Bytes())
	if got != "# hello" {
		t.Errorf("expected '# hello', got %q", got)
	}
}

func TestNodeBytesConcatenatesChildren(t *testing.T) {
	node := &Node{
		Kind: NodeKeyValue,
		Children: []*Node{
			{Kind: NodeKey, Raw: []byte("key")},
			{Kind: NodeWhitespace, Raw: []byte(" ")},
			{Kind: NodeEquals, Raw: []byte("=")},
			{Kind: NodeWhitespace, Raw: []byte(" ")},
			{Kind: NodeString, Raw: []byte(`"value"`)},
			{Kind: NodeNewline, Raw: []byte("\n")},
		},
	}
	got := string(node.Bytes())
	expected := "key = \"value\"\n"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}
