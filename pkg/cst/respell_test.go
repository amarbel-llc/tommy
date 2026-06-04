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

	t.Run("two-segment leaf table -> inline into parent body", func(t *testing.T) {
		// [direnv] then [direnv.dotenv] -> dotenv = {...} appended into [direnv].
		assertRespell(t, RespellInlineTables,
			"[direnv]\n[direnv.dotenv]\nFOO = \"bar\"\n",
			"[direnv]\ndotenv = { FOO = \"bar\" }\n",
		)
	})

	t.Run("preserves a leading scalar field on the parent", func(t *testing.T) {
		assertRespell(t, RespellInlineTables,
			"[direnv]\nname = \"x\"\n[direnv.dotenv]\nFOO = \"bar\"\n",
			"[direnv]\nname = \"x\"\ndotenv = { FOO = \"bar\" }\n",
		)
	})

	t.Run("non-leaf super-table left canonical", func(t *testing.T) {
		// [a] owns [a.b], so [a] cannot be inlined; [a.b] is a leaf two-segment
		// table whose parent [a] IS present, so it inlines into [a].
		assertRespell(t, RespellInlineTables,
			"[a]\n[a.b]\nk = \"v\"\n",
			"[a]\nb = { k = \"v\" }\n",
		)
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

	t.Run("inlined root key-value is hoisted above table headers", func(t *testing.T) {
		// A single-segment table appearing AFTER a sub-table must inline to a root
		// key-value placed BEFORE the first header — otherwise the bare `env = {}`
		// would bind to the preceding [other.sub] table, not the document (#107
		// regression: the first 96-case run caught exactly this).
		assertRespell(t, RespellInlineTables,
			"[other]\n[other.sub]\nk = \"v\"\n[env]\nFOO = \"bar\"\n",
			"env = { FOO = \"bar\" }\n[other]\nsub = { k = \"v\" }\n",
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
