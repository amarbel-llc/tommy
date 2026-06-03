package cst

import (
	"strings"
	"testing"
)

// TestTableHeaderSegments verifies that a quoted segment containing a dot or a
// space is returned as one segment, unlike TableHeaderKey's lossy join (#103).
func TestTableHeaderSegments(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{"[a.b.c]\n", []string{"a", "b", "c"}},
		{"[m.\"a.b\"]\n", []string{"m", "a.b"}},
		{"[m.\"a.b\".sub]\n", []string{"m", "a.b", "sub"}},
		{"[\"k 1\"]\n", []string{"k 1"}},
		{"[[srv.\"a.b\"]]\n", []string{"srv", "a.b"}},
	}
	for _, tc := range cases {
		root := mustParseRoot(t, tc.src)
		var tbl *Node
		for _, c := range root.Children {
			if c.Kind == NodeTable || c.Kind == NodeArrayTable {
				tbl = c
				break
			}
		}
		if tbl == nil {
			t.Fatalf("no table node parsed from %q", tc.src)
		}
		got := TableHeaderSegments(tbl)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("TableHeaderSegments(%q) = %v, want %v", tc.src, got, tc.want)
		}
	}
}

// TestHeaderKeyNodesRoundTrip verifies headerKeyNodes quotes only the segments
// that need it and that TableHeaderSegments recovers the originals — the
// encode→decode inverse relationship the #103 fix relies on.
func TestHeaderKeyNodesRoundTrip(t *testing.T) {
	segs := []string{"m", "a.b", "k 1", "plain"}
	nodes := headerKeyNodes(segs)
	tbl := &Node{Kind: NodeTable}
	tbl.Children = append(tbl.Children, &Node{Kind: NodeBracketOpen, Raw: []byte("[")})
	tbl.Children = append(tbl.Children, nodes...)
	tbl.Children = append(tbl.Children, &Node{Kind: NodeBracketClose, Raw: []byte("]")})

	wantRaw := `[m."a.b"."k 1".plain]`
	if got := string(tbl.Bytes()); got != wantRaw {
		t.Fatalf("rendered header = %q, want %q", got, wantRaw)
	}
	got := TableHeaderSegments(tbl)
	if strings.Join(got, "|") != strings.Join(segs, "|") {
		t.Fatalf("round-trip segments = %v, want %v", got, segs)
	}
}
