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

func TestFindChildTableTopLevelEntries(t *testing.T) {
	root := mustParseRoot(t, `[[servers]]
name = "alpha"

[servers.settings]
timeout = 30

[[servers]]
name = "beta"

[servers.settings]
timeout = 60
`)
	for i, want := range []string{"servers.settings", "servers.settings"} {
		entry := nthArrayTable(root, "servers", i)
		if entry == nil {
			t.Fatalf("entry %d missing", i)
		}
		got := FindChildTable(root, entry, "settings")
		if got == nil {
			t.Fatalf("entry %d: settings not found", i)
		}
		if TableHeaderKey(got) != want {
			t.Fatalf("entry %d: header=%q", i, TableHeaderKey(got))
		}
		// The two entries must resolve to *distinct* nested tables, not the same one.
		if i == 1 {
			other := FindChildTable(root, nthArrayTable(root, "servers", 0), "settings")
			if got == other {
				t.Fatal("both entries resolved to the same [servers.settings] node")
			}
		}
	}
}

func TestFindChildTableAbsent(t *testing.T) {
	root := mustParseRoot(t, `[[servers]]
name = "alpha"

[[servers]]
name = "beta"

[servers.settings]
timeout = 60
`)
	// The first entry has no [servers.settings]; the scope must not leak into the
	// second entry's table.
	if got := FindChildTable(root, nthArrayTable(root, "servers", 0), "settings"); got != nil {
		t.Fatalf("entry 0 should have no settings, got %q", TableHeaderKey(got))
	}
	if got := FindChildTable(root, nthArrayTable(root, "servers", 1), "settings"); got == nil {
		t.Fatal("entry 1 settings not found")
	}
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
	// second outer's inner meta (parentScope would over-extend here).
	inner00 := FindChildArrayTableNodes(root, nthArrayTable(root, "outers", 0), "inners")[0]
	meta := FindChildTable(root, inner00, "meta")
	if meta == nil {
		t.Fatal("inner00 meta not found")
	}
	// Confirm it is m00 by reading the key-value.
	if v, ok := ExtractString(findKV(meta, "key")); !ok || v != "m00" {
		t.Fatalf("inner00 meta.key = %q (ok=%v), want m00", v, ok)
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
