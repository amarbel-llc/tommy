package cst

import "testing"

// leafScalar parses a single `key = <scalar/array>` document and returns the
// source NodeKeyValue for the leaf, the node the Extract*Slice helpers operate
// on.
func leafScalar(t *testing.T, src, key string) *Node {
	t.Helper()
	root, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	model, err := Decompose(root)
	if err != nil {
		t.Fatalf("Decompose(%q): %v", src, err)
	}
	v, ok := model.Get(key)
	if !ok || v.Kind != VLeaf {
		t.Fatalf("%q: key %q is not a scalar leaf", src, key)
	}
	return v.Leaf
}

// A homogeneous (or integer-widened-to-float) array still extracts cleanly, and
// an empty array yields an empty slice with ok=true.
func TestExtractSliceStrictAccepts(t *testing.T) {
	if got, ok := ExtractIntSlice(leafScalar(t, "xs = [1, 2, 3]\n", "xs")); !ok || len(got) != 3 {
		t.Fatalf("ExtractIntSlice([1,2,3]) = %v, %v; want len 3, true", got, ok)
	}
	if got, ok := ExtractStringSlice(leafScalar(t, "xs = [\"a\", \"b\"]\n", "xs")); !ok || len(got) != 2 {
		t.Fatalf("ExtractStringSlice = %v, %v; want len 2, true", got, ok)
	}
	// ExtractFloat64Slice deliberately accepts integer-valued elements.
	if got, ok := ExtractFloat64Slice(leafScalar(t, "xs = [1, 2.5]\n", "xs")); !ok || len(got) != 2 {
		t.Fatalf("ExtractFloat64Slice([1, 2.5]) = %v, %v; want len 2, true", got, ok)
	}
	if got, ok := ExtractIntSlice(leafScalar(t, "xs = []\n", "xs")); !ok || len(got) != 0 {
		t.Fatalf("ExtractIntSlice([]) = %v, %v; want empty, true", got, ok)
	}
}

// A heterogeneous array — an element of the wrong kind — returns ok=false rather
// than silently dropping the mismatched element, so the decoder surfaces a type
// error instead of a partial slice (review #2/#6).
func TestExtractSliceStrictRejectsMixed(t *testing.T) {
	cases := []struct {
		name string
		src  string
		ok   func(*Node) bool
	}{
		{"int slice with a float", "xs = [1, 2.5, 3]\n", func(n *Node) bool { _, ok := ExtractIntSlice(n); return ok }},
		{"int slice with a string", "xs = [1, \"x\"]\n", func(n *Node) bool { _, ok := ExtractIntSlice(n); return ok }},
		{"int slice with an inline table", "xs = [{ a = 1 }, 2]\n", func(n *Node) bool { _, ok := ExtractIntSlice(n); return ok }},
		{"string slice with an int", "xs = [\"a\", 1]\n", func(n *Node) bool { _, ok := ExtractStringSlice(n); return ok }},
		{"bool slice with an int", "xs = [true, 1]\n", func(n *Node) bool { _, ok := ExtractBoolSlice(n); return ok }},
		{"float slice with a string", "xs = [1.0, \"x\"]\n", func(n *Node) bool { _, ok := ExtractFloat64Slice(n); return ok }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ok(leafScalar(t, tc.src, "xs")) {
				t.Fatalf("%q: extractor returned ok=true for a heterogeneous array (want false)", tc.src)
			}
		})
	}
}
