package cst

import (
	"strings"
	"testing"
)

func TestCheckNoDuplicateKeys(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantDup string // expected key in the error, or "" for no error
	}{
		// #110: the inline-table outer key written twice — the gap none of the
		// distributed decoder guards covered.
		{"inline outer key twice", "mytable = { a = 1 }\nmytable = { a = 2 }\n", "mytable"},
		// #90 dual: duplicate scalar key at root.
		{"scalar key twice", "a = 1\na = 2\n", "a"},
		// Duplicate key within one inline table.
		{"inline inner key twice", "m = { a = 1, a = 2 }\n", "a"},
		// Duplicate key in a table body.
		{"table body key twice", "[t]\nx = 1\nx = 2\n", "x"},
		// Duplicate key inside an inline table within an inline array.
		{"inline array element key twice", "xs = [ { h = 1, h = 2 } ]\n", "h"},

		// Valid: same key in different scopes is fine.
		{"same key different tables", "[a]\nk = 1\n[b]\nk = 2\n", ""},
		{"same key different array entries", "[[e]]\nk = 1\n[[e]]\nk = 2\n", ""},
		{"distinct inline keys", "m = { a = 1, b = 2 }\n", ""},
		{"nested distinct", "s = { name = \"a\", inner = { x = 1 } }\n", ""},
		// Repeated table HEADERS are not a "duplicate key" here — left to the
		// #92/#102 guards. CheckNoDuplicateKeys must NOT flag them.
		{"repeated table header not flagged", "[t]\nx = 1\n[t]\ny = 2\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := Parse([]byte(tc.input))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			err = CheckNoDuplicateKeys(root)
			if tc.wantDup == "" {
				if err != nil {
					t.Fatalf("CheckNoDuplicateKeys = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckNoDuplicateKeys = nil, want duplicate %q", tc.wantDup)
			}
			if !strings.Contains(err.Error(), "duplicate key") || !strings.Contains(err.Error(), tc.wantDup) {
				t.Fatalf("error %q should mention duplicate key %q", err.Error(), tc.wantDup)
			}
		})
	}
}
