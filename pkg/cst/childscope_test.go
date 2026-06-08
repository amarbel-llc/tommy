package cst

import "testing"

// helper: parse and return the root node.
func mustParseRoot(t *testing.T, src string) *Node {
	t.Helper()
	root, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return root
}

// firstArrayTable returns the n-th (0-indexed) [[key]] entry under root.
func nthArrayTable(root *Node, key string, n int) *Node {
	nodes := FindArrayTableNodes(root, key)
	if n >= len(nodes) {
		return nil
	}
	return nodes[n]
}

func TestFindChildArrayTableNodesNested(t *testing.T) {
	root := mustParseRoot(t, `[[outers]]
name = "o0"

[[outers.inners]]
id = "i00"

[[outers.inners]]
id = "i01"

[[outers]]
name = "o1"

[[outers.inners]]
id = "i10"
`)
	o0 := nthArrayTable(root, "outers", 0)
	o1 := nthArrayTable(root, "outers", 1)
	if got := FindChildArrayTableNodes(root, o0, "inners"); len(got) != 2 {
		t.Fatalf("o0 inners = %d, want 2", len(got))
	}
	got1 := FindChildArrayTableNodes(root, o1, "inners")
	if len(got1) != 1 {
		t.Fatalf("o1 inners = %d, want 1 (scope leaked across outer)", len(got1))
	}
}

func TestChildScopeBoundsNestedEntry(t *testing.T) {
	root := mustParseRoot(t, `[[outers]]
name = "o0"

[[outers.inners]]
id = "i00"

[outers.inners.meta]
key = "m00"

[[outers]]
name = "o1"

[[outers.inners]]
id = "i10"

[outers.inners.meta]
key = "m10"
`)
	// The first inner of the first outer must resolve its own meta, not the
	// second outer's inner meta (parentScope would over-extend here). Scan only
	// within inner00's ChildScope so the second outer's [outers.inners.meta] is
	// out of bounds.
	inner00 := FindChildArrayTableNodes(root, nthArrayTable(root, "outers", 0), "inners")[0]
	start, end := ChildScope(root, inner00)
	var meta *Node
	for i := start; i < end; i++ {
		if c := root.Children[i]; c.Kind == NodeTable && TableHeaderKey(c) == "outers.inners.meta" {
			meta = c
			break
		}
	}
	if meta == nil {
		t.Fatal("inner00 meta not found")
	}
	// Confirm it is m00 by reading the key-value.
	if v, ok := ExtractString(findKV(meta, "key")); !ok || v != "m00" {
		t.Fatalf("inner00 meta.key = %q (ok=%v), want m00", v, ok)
	}
}

func TestAppendChildArrayTableEntryScoped(t *testing.T) {
	// Two outers; outer 0 already has one inner. Appending an inner to each outer
	// must place it within that outer's scope, not at the document end.
	root := mustParseRoot(t, `[[outers]]
name = "o0"

[[outers.inners]]
id = "i00"

[[outers]]
name = "o1"
`)
	o0 := nthArrayTable(root, "outers", 0)
	o1 := nthArrayTable(root, "outers", 1)

	n0 := AppendChildArrayTableEntry(root, o0, "inners")
	if got := TableHeaderKey(n0); got != "outers.inners" {
		t.Fatalf("appended header = %q", got)
	}
	// o0 now has two inners, o1 still has none — i.e. the append landed inside o0.
	if got := len(FindChildArrayTableNodes(root, o0, "inners")); got != 2 {
		t.Fatalf("o0 inners after append = %d, want 2", got)
	}
	if got := len(FindChildArrayTableNodes(root, o1, "inners")); got != 0 {
		t.Fatalf("o1 inners after append = %d, want 0 (append leaked across outer)", got)
	}

	// Appending to o1 (which had none) lands within o1's scope.
	AppendChildArrayTableEntry(root, o1, "inners")
	if got := len(FindChildArrayTableNodes(root, o1, "inners")); got != 1 {
		t.Fatalf("o1 inners after second append = %d, want 1", got)
	}
}

// findKV returns the NodeKeyValue with the given name under container.
func findKV(container *Node, name string) *Node {
	for _, c := range container.Children {
		if c.Kind == NodeKeyValue && KeyValueName(c) == name {
			return c
		}
	}
	return nil
}
