package cst

import (
	"bytes"
	"testing"
)

// respellNoChange reports whether respelling produced byte-identical output —
// used to assert a transform was a no-op on inapplicable input.
func respellNoChange(in, out []byte) bool { return bytes.Equal(in, out) }

// respell asserts a Respell* function turns `in` into exactly `want`, that the
// output re-parses, and (implicitly) that it is valid TOML (Parse succeeds).
func assertRespell(t *testing.T, fn func([]byte) ([]byte, error), in, want string) {
	t.Helper()
	got, err := fn([]byte(in))
	if err != nil {
		t.Fatalf("respell error: %v", err)
	}
	if string(got) != want {
		t.Fatalf("respell mismatch\n in:   %q\n got:  %q\n want: %q", in, string(got), want)
	}
	if _, err := Parse(got); err != nil {
		t.Fatalf("respelled output does not re-parse: %v\n%s", err, got)
	}
}

func TestRespellInlineTables(t *testing.T) {
	t.Run("single-segment leaf table -> inline at root", func(t *testing.T) {
		assertRespell(t, RespellInlineTables,
			"[env]\nFOO = \"bar\"\nBAZ = \"qux\"\n",
			"env = { FOO = \"bar\", BAZ = \"qux\" }\n",
		)
	})

	t.Run("two-level subtree -> nested inline value", func(t *testing.T) {
		// [direnv] + [direnv.dotenv] folds into the whole subtree as one nested
		// inline value (#111 deep-inline), not a partial header + inline-inner.
		assertRespell(t, RespellInlineTables,
			"[direnv]\n[direnv.dotenv]\nFOO = \"bar\"\n",
			"direnv = { dotenv = { FOO = \"bar\" } }\n",
		)
	})

	t.Run("leading scalar field folds before the sub-table", func(t *testing.T) {
		assertRespell(t, RespellInlineTables,
			"[direnv]\nname = \"x\"\n[direnv.dotenv]\nFOO = \"bar\"\n",
			"direnv = { name = \"x\", dotenv = { FOO = \"bar\" } }\n",
		)
	})

	t.Run("three-level subtree -> fully nested inline value", func(t *testing.T) {
		// The #111 target: deep nesting reaches a fully-inline nested struct.
		assertRespell(t, RespellInlineTables,
			"[a]\n[a.b]\n[a.b.c]\nk = \"v\"\n",
			"a = { b = { c = { k = \"v\" } } }\n",
		)
	})

	t.Run("non-leaf super-table deep-inlined", func(t *testing.T) {
		// [a] owns [a.b]: the whole subtree folds into one nested inline value.
		assertRespell(t, RespellInlineTables,
			"[a]\n[a.b]\nk = \"v\"\n",
			"a = { b = { k = \"v\" } }\n",
		)
	})

	t.Run("folds an implicit-intermediate map field (value-preserving)", func(t *testing.T) {
		// A map field's entries appear as deeper headers ([a.m.key]) with no bare
		// [a.m] header. Deep-inlining [a] must FOLD them (group by the implicit
		// "m" segment), not drop them — the rewrite is value-preserving.
		assertRespell(t, RespellInlineTables,
			"[a]\nname = \"x\"\n[a.m.k1]\nik = \"v1\"\n[a.m.k2]\nik = \"v2\"\n",
			"a = { name = \"x\", m = { k1 = { ik = \"v1\" }, k2 = { ik = \"v2\" } } }\n",
		)
	})

	t.Run("subtree with an array-table descendant left canonical", func(t *testing.T) {
		// [[a.xs]] cannot sit inside an inline table (that is RespellInlineArrays'
		// dual), so the whole [a] subtree declines and stays canonical.
		in := "[a]\nname = \"x\"\n[[a.xs]]\nh = \"1\"\n"
		got, err := RespellInlineTables([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op (array-table descendant), got %q", got)
		}
	})

	t.Run("top-level map[string]struct (no parent header) left canonical", func(t *testing.T) {
		in := "[actions.build]\ncommand = \"make\"\n"
		got, err := RespellInlineTables([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op (parent header absent), got %q", got)
		}
	})

	t.Run("no tables -> no-op", func(t *testing.T) {
		in := "name = \"x\"\nport = 8080\n"
		got, _ := RespellInlineTables([]byte(in))
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op, got %q", got)
		}
	})

	t.Run("quoted value bytes preserved verbatim", func(t *testing.T) {
		// A value with an embedded escape must survive byte-for-byte (no re-render).
		assertRespell(t, RespellInlineTables,
			"[env]\nK = \"a\\tb\"\n",
			"env = { K = \"a\\tb\" }\n",
		)
	})

	t.Run("multiline-string value left canonical (newline invalid in inline table)", func(t *testing.T) {
		// A multiline basic string carries literal newlines in its Raw bytes;
		// inlining it would put a newline inside `{ }`, which TOML 1.0 forbids. The
		// rewrite must decline and leave the table canonical.
		in := "[hooks]\ncreate = \"\"\"line1\nline2\"\"\"\n"
		got, err := RespellInlineTables([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op (multiline value), got %q", got)
		}
	})

	t.Run("empty table -> empty inline table", func(t *testing.T) {
		// An empty [env] is a leaf too; it inlines to `env = {}`. Empties must fire
		// deterministically (an empty array-table entry is a runtime value property
		// the shape-based fuzzer cannot foresee).
		assertRespell(t, RespellInlineTables, "[env]\n", "env = {}\n")
	})

	t.Run("inlined subtrees fold to root key-values in leader order", func(t *testing.T) {
		// Both [other] (with its [other.sub]) and [env] fold to root key-values;
		// each is hoisted before any remaining header (here none remain), so a bare
		// root key-value never binds to a preceding table (#107 regression). Order
		// follows the single-segment leaders' document order.
		assertRespell(t, RespellInlineTables,
			"[other]\n[other.sub]\nk = \"v\"\n[env]\nFOO = \"bar\"\n",
			"other = { sub = { k = \"v\" } }\nenv = { FOO = \"bar\" }\n",
		)
	})
}

func TestRespellDottedKeys(t *testing.T) {
	t.Run("single-segment leaf table -> dotted keys", func(t *testing.T) {
		assertRespell(t, RespellDottedKeys,
			"[inner]\nname = \"a\"\nport = 8080\n",
			"inner.name = \"a\"\ninner.port = 8080\n",
		)
	})

	t.Run("super-table left canonical (would redefine table)", func(t *testing.T) {
		in := "[a]\n[a.b]\nk = \"v\"\n"
		got, err := RespellDottedKeys([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		// [a] owns [a.b] (prefix child) so [a] is skipped; [a.b] is two-segment so
		// also skipped. Whole doc canonical.
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op, got %q", got)
		}
	})

	t.Run("two-segment table left canonical", func(t *testing.T) {
		in := "[direnv.dotenv]\nFOO = \"bar\"\n"
		got, _ := RespellDottedKeys([]byte(in))
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op, got %q", got)
		}
	})

	t.Run("no-op when nothing qualifies", func(t *testing.T) {
		in := "k = \"v\"\n"
		got, _ := RespellDottedKeys([]byte(in))
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op, got %q", got)
		}
	})

	t.Run("empty table left canonical (no keys to dot)", func(t *testing.T) {
		// Dotted keys can't spell an empty table, so an empty [env] must stay
		// canonical rather than vanish.
		in := "[env]\n"
		got, _ := RespellDottedKeys([]byte(in))
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op, got %q", got)
		}
	})
}

func TestRespellDottedKeysHoistsBelowLateTable(t *testing.T) {
	// A single-segment leaf table positioned AFTER another table must hoist its
	// dotted keys ABOVE the first header — a root `a.x = v` after `[other]` would
	// otherwise bind under [other], changing the value.
	assertRespell(t, RespellDottedKeys,
		"[[other]]\nk = \"v\"\n[a]\nx = 1\n",
		"a.x = 1\n[[other]]\nk = \"v\"\n",
	)
}

func TestRespellInlineArraysHoistsBelowLateTable(t *testing.T) {
	assertRespell(t, RespellInlineArrays,
		"[other]\nk = \"v\"\n[[xs]]\nh = \"a\"\n",
		"xs = [ { h = \"a\" } ]\n[other]\nk = \"v\"\n",
	)
}

func TestRespellImplicitParentsArrayScopeSafe(t *testing.T) {
	// An empty map-entry table in one [[xs]] entry must NOT be dropped just
	// because a SIBLING entry has a same-keyed entry with deeper children: the
	// "immediately-following header extends it" rule keeps it (its own scope has
	// no deeper child). Only [xs.m.b], whose own sub-table follows it, collapses.
	assertRespell(t, RespellImplicitParents,
		"[[xs]]\n[xs.m.a]\n[xs.m.b]\n[xs.m.b.inner]\nk = \"v\"\n[[xs]]\n[xs.m.a]\n[xs.m.a.inner]\nk = \"w\"\n",
		"[[xs]]\n[xs.m.a]\n[xs.m.b.inner]\nk = \"v\"\n[[xs]]\n[xs.m.a.inner]\nk = \"w\"\n",
	)
}

func TestRespellImplicitParents(t *testing.T) {
	t.Run("empty parent of a sub-table is dropped", func(t *testing.T) {
		assertRespell(t, RespellImplicitParents,
			"[direnv]\n[direnv.dotenv]\nFOO = \"bar\"\n",
			"[direnv.dotenv]\nFOO = \"bar\"\n",
		)
	})

	t.Run("empty parent of an array-table is dropped", func(t *testing.T) {
		assertRespell(t, RespellImplicitParents,
			"[srv]\n[[srv.hosts]]\nname = \"a\"\n",
			"[[srv.hosts]]\nname = \"a\"\n",
		)
	})

	t.Run("chain of empty parents collapses", func(t *testing.T) {
		assertRespell(t, RespellImplicitParents,
			"[a]\n[a.b]\n[a.b.c]\nk = \"v\"\n",
			"[a.b.c]\nk = \"v\"\n",
		)
	})

	t.Run("parent with its own key-values is kept", func(t *testing.T) {
		in := "[direnv]\nname = \"x\"\n[direnv.dotenv]\nFOO = \"bar\"\n"
		got, err := RespellImplicitParents([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op (parent has key-values), got %q", got)
		}
	})

	t.Run("leaf table with no children left canonical", func(t *testing.T) {
		in := "[env]\nFOO = \"bar\"\n"
		got, _ := RespellImplicitParents([]byte(in))
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op (no sub-tables), got %q", got)
		}
	})

	t.Run("no tables -> no-op", func(t *testing.T) {
		in := "name = \"x\"\n"
		got, _ := RespellImplicitParents([]byte(in))
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op, got %q", got)
		}
	})
}

func TestRespellInlineArrays(t *testing.T) {
	t.Run("two leaf entries -> inline array of inline tables", func(t *testing.T) {
		assertRespell(t, RespellInlineArrays,
			"[[servers]]\nhost = \"a\"\n[[servers]]\nhost = \"b\"\n",
			"servers = [ { host = \"a\" }, { host = \"b\" } ]\n",
		)
	})

	t.Run("single entry", func(t *testing.T) {
		assertRespell(t, RespellInlineArrays,
			"[[servers]]\nhost = \"a\"\n",
			"servers = [ { host = \"a\" } ]\n",
		)
	})

	t.Run("empty entry inlines to empty table", func(t *testing.T) {
		// A zero-valued [[servers]] entry has no body; it must inline to `{}` so the
		// array rewrite fires deterministically regardless of entry contents.
		assertRespell(t, RespellInlineArrays,
			"[[servers]]\n[[servers]]\nhost = \"b\"\n",
			"servers = [ {}, { host = \"b\" } ]\n",
		)
	})

	t.Run("entry with multiline value left canonical", func(t *testing.T) {
		// A multiline string in an entry can't go inside an inline table; the whole
		// array rewrite must decline.
		in := "[[servers]]\nscript = \"\"\"a\nb\"\"\"\n"
		got, err := RespellInlineArrays([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op (multiline entry value), got %q", got)
		}
	})

	t.Run("entry with nested sub-table left canonical", func(t *testing.T) {
		in := "[[servers]]\nhost = \"a\"\n[servers.meta]\nk = \"v\"\n"
		got, err := RespellInlineArrays([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op (non-leaf entry), got %q", got)
		}
	})

	t.Run("no array-tables -> no-op", func(t *testing.T) {
		in := "[env]\nk = \"v\"\n"
		got, _ := RespellInlineArrays([]byte(in))
		if !respellNoChange([]byte(in), got) {
			t.Fatalf("expected no-op, got %q", got)
		}
	})
}

// Respelling must preserve the decoded values: the canonical and respelled forms
// extract to the same map[string]string under a table/inline-table.
func TestRespellPreservesValues(t *testing.T) {
	got, err := RespellInlineTables([]byte("[env]\nFOO = \"bar\"\nBAZ = \"qux\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	root := mustParseRoot(t, string(got))
	it := FindChildInlineTable(root, "env")
	if it == nil {
		t.Fatal("expected env inline table in respelled output")
	}
	m := ExtractStringMap(it)
	if m["FOO"] != "bar" || m["BAZ"] != "qux" {
		t.Fatalf("values not preserved: %v", m)
	}
}
