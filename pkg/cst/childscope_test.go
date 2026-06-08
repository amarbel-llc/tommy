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

// FindChildInlineTable is the inline-spelling dual of FindChildTable (#106): a
// map[string]string field may be written as `k = { ... }` instead of a
// [parent.k] sub-table, and both must resolve.
func TestFindChildInlineTable(t *testing.T) {
	t.Run("top-level inline table", func(t *testing.T) {
		root := mustParseRoot(t, "env = { FOO = \"bar\", BAZ = \"qux\" }\n")
		it := FindChildInlineTable(root, "env")
		if it == nil {
			t.Fatal("env inline table not found")
		}
		if it.Kind != NodeInlineTable {
			t.Fatalf("kind = %v, want NodeInlineTable", it.Kind)
		}
		m := ExtractStringMap(it)
		if m["FOO"] != "bar" || m["BAZ"] != "qux" {
			t.Fatalf("extracted %v", m)
		}
	})

	t.Run("inline table nested under a parent table", func(t *testing.T) {
		root := mustParseRoot(t, "[direnv]\ndotenv = { FOO = \"bar\" }\n")
		direnv := findTableNode(root, "direnv")
		if direnv == nil {
			t.Fatal("[direnv] table not found")
		}
		// It must resolve relative to the parent table node, not the root: the
		// inline kv is a child of [direnv], so a root lookup must miss it.
		if FindChildInlineTable(root, "dotenv") != nil {
			t.Fatal("dotenv must not resolve at root — it lives inside [direnv]")
		}
		it := FindChildInlineTable(direnv, "dotenv")
		if it == nil {
			t.Fatal("dotenv inline table not found under [direnv]")
		}
		if m := ExtractStringMap(it); m["FOO"] != "bar" {
			t.Fatalf("extracted %v", m)
		}
	})

	t.Run("absent key returns nil", func(t *testing.T) {
		root := mustParseRoot(t, "other = { A = \"b\" }\n")
		if FindChildInlineTable(root, "env") != nil {
			t.Fatal("absent key should return nil")
		}
	})

	t.Run("non-inline value returns nil", func(t *testing.T) {
		// A scalar value with the matching key is not an inline table; the
		// generated decoder relies on this returning nil so it does not mistake a
		// plain key for a map.
		root := mustParseRoot(t, "env = \"not-a-table\"\n")
		if FindChildInlineTable(root, "env") != nil {
			t.Fatal("scalar value must not match as an inline table")
		}
	})
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

// findTableNode returns the first NodeTable under root with the given header.
func findTableNode(root *Node, header string) *Node {
	for _, c := range root.Children {
		if c.Kind == NodeTable && TableHeaderKey(c) == header {
			return c
		}
	}
	return nil
}
