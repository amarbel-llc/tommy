package cst

import (
	"sort"
	"strings"
	"testing"
)

// dumpValue renders a Value as a canonical, order-insensitive string so that two
// models built from DIFFERENT spellings of the same value compare equal. Leaves
// render as their scalar value bytes; tables sort their keys; arrays keep order.
func dumpValue(v *Value) string {
	switch v.Kind {
	case VLeaf:
		return "L(" + strings.TrimSpace(string(KeyValueValue(v.Leaf).Bytes())) + ")"
	case VArray:
		var parts []string
		for i := range v.Items {
			parts = append(parts, dumpValue(&v.Items[i]))
		}
		return "[" + strings.Join(parts, ",") + "]"
	default: // VTable
		var parts []string
		for i := range v.Fields {
			parts = append(parts, v.Fields[i].Key+"="+dumpValue(&v.Fields[i].Val))
		}
		sort.Strings(parts)
		return "{" + strings.Join(parts, ",") + "}"
	}
}

func mustDecompose(t *testing.T, src string) *Value {
	t.Helper()
	root, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	v, err := Decompose(root)
	if err != nil {
		t.Fatalf("Decompose(%q): %v", src, err)
	}
	return v
}

// The core claim of the normalization ADR: every legal spelling of a value
// decomposes to the SAME canonical model. Each group lists spellings that must
// collapse together.
func TestDecomposeSpellingsCollapse(t *testing.T) {
	groups := []struct {
		name      string
		want      string
		spellings []string
	}{
		{
			name: "nested struct",
			want: "{inner={deep={x=L(5)},name=L(\"a\")}}",
			spellings: []string{
				"[inner]\nname = \"a\"\n[inner.deep]\nx = 5\n", // header tables
				"inner = { name = \"a\", deep = { x = 5 } }\n", // fully inline
				"inner.name = \"a\"\ninner.deep.x = 5\n",       // dotted keys
				"[inner]\nname = \"a\"\ndeep = { x = 5 }\n",    // header + inline-inner (#115)
				"[inner]\nname = \"a\"\ndeep.x = 5\n",          // header + dotted-inner
			},
		},
		{
			name: "array of tables",
			want: "{xs=[{h=L(\"1\")},{h=L(\"2\")}]}",
			spellings: []string{
				"[[xs]]\nh = \"1\"\n[[xs]]\nh = \"2\"\n",  // array-of-tables
				"xs = [ { h = \"1\" }, { h = \"2\" } ]\n", // inline array of inline tables
			},
		},
		{
			name: "implicit parent",
			want: "{a={b={x=L(1)}}}",
			spellings: []string{
				"[a]\n[a.b]\nx = 1\n", // explicit parent
				"[a.b]\nx = 1\n",      // implicit parent (#113)
			},
		},
		{
			name: "map[string]string",
			want: "{env={K=L(\"v\")}}",
			spellings: []string{
				"[env]\nK = \"v\"\n",    // sub-table
				"env = { K = \"v\" }\n", // inline (#106)
				"env.K = \"v\"\n",       // dotted key
			},
		},
		{
			name: "map of structs, entry via deeper header (implicit entry)",
			want: "{m={k={f={x=L(1)}}}}",
			spellings: []string{
				"[m.k]\n[m.k.f]\nx = 1\n",         // explicit entry + sub-table
				"[m.k.f]\nx = 1\n",                // implicit entry (#114/#117 frontier)
				"m = { k = { f = { x = 1 } } }\n", // fully inline
			},
		},
	}
	for _, g := range groups {
		t.Run(g.name, func(t *testing.T) {
			for _, s := range g.spellings {
				got := dumpValue(mustDecompose(t, s))
				if got != g.want {
					t.Fatalf("spelling collapse mismatch\n  spelling: %q\n  got:  %s\n  want: %s", s, got, g.want)
				}
			}
		})
	}
}

func TestDecomposeRejectsDuplicates(t *testing.T) {
	bad := []struct{ name, src, msg string }{
		{"scalar key twice", "a = 1\na = 2\n", "duplicate key"},
		{"inline inner key twice", "m = { a = 1, a = 2 }\n", "duplicate key"},
		{"table defined twice", "[t]\nx = 1\n[t]\ny = 2\n", "duplicate table"},
		{"leaf then table", "a = 1\n[a]\nx = 1\n", "not a table"},
		{"dotted then leaf", "a.b = 1\na = 2\n", "duplicate key"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			root, err := Parse([]byte(tc.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			_, err = Decompose(root)
			if err == nil {
				t.Fatalf("expected error for %q", tc.src)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("error %q should contain %q", err.Error(), tc.msg)
			}
		})
	}
}

func TestDecomposeAcceptsValid(t *testing.T) {
	// Same key in different scopes is fine; present-but-empty is preserved.
	ok := []string{
		"[a]\nk = 1\n[b]\nk = 2\n",     // same key, different tables
		"[[e]]\nk = 1\n[[e]]\nk = 2\n", // same key, different array entries
		"[empty]\n",                    // present-but-empty table
		"xs = []\n",                    // present-but-empty array (a scalar-array leaf)
		"m = {}\n",                     // present-but-empty inline table
	}
	for _, s := range ok {
		root, err := Parse([]byte(s))
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if _, err := Decompose(root); err != nil {
			t.Fatalf("Decompose(%q): unexpected error %v", s, err)
		}
	}
}
