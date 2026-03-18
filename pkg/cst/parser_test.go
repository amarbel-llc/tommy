package cst

import (
	"bytes"
	"testing"
)

func assertRoundTrip(t *testing.T, input string, doc *Node) {
	t.Helper()
	got := doc.Bytes()
	if !bytes.Equal(got, []byte(input)) {
		t.Errorf("round-trip failed:\n  input: %q\n  got:   %q", input, string(got))
	}
}

func assertDocKind(t *testing.T, doc *Node) {
	t.Helper()
	if doc.Kind != NodeDocument {
		t.Fatalf("expected NodeDocument, got %d", doc.Kind)
	}
}

func TestParseSimpleKeyValue(t *testing.T) {
	input := "key = \"value\"\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	assertDocKind(t, doc)
	assertRoundTrip(t, input, doc)

	// Should contain a NodeKeyValue child (among possible trivia)
	found := false
	for _, child := range doc.Children {
		if child.Kind == NodeKeyValue {
			found = true
		}
	}
	if !found {
		t.Error("expected NodeKeyValue child in document")
	}
}

func TestParseCommentPreserved(t *testing.T) {
	input := "# a comment\nkey = \"value\"\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	assertDocKind(t, doc)
	assertRoundTrip(t, input, doc)
}

func TestParseTableWithComments(t *testing.T) {
	input := "# file header\n\n[server]\n# host comment\nhost = \"localhost\"\nport = 8080 # trailing\n\n[database]\nname = \"mydb\"\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	assertDocKind(t, doc)
	assertRoundTrip(t, input, doc)
}

func TestParseArrayTable(t *testing.T) {
	input := "[[items]]\nname = \"item1\"\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	assertDocKind(t, doc)
	assertRoundTrip(t, input, doc)

	found := false
	for _, child := range doc.Children {
		if child.Kind == NodeArrayTable {
			found = true
		}
	}
	if !found {
		t.Error("expected NodeArrayTable child in document")
	}
}

func TestParseNestedTables(t *testing.T) {
	input := "[a]\nk = 1\n[a.b]\nk = 2\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	assertDocKind(t, doc)
	assertRoundTrip(t, input, doc)

	tableCount := 0
	for _, child := range doc.Children {
		if child.Kind == NodeTable {
			tableCount++
		}
	}
	if tableCount != 2 {
		t.Errorf("expected 2 NodeTable children, got %d", tableCount)
	}
}

func TestParseDottedKeys(t *testing.T) {
	input := "a.b.c = \"val\"\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	assertDocKind(t, doc)
	assertRoundTrip(t, input, doc)
}

func TestParseMultilineStrings(t *testing.T) {
	input := "desc = \"\"\"hello\nworld\"\"\"\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	assertDocKind(t, doc)
	assertRoundTrip(t, input, doc)
}

func TestParseInlineTable(t *testing.T) {
	input := "point = {x = 1, y = 2}\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	assertDocKind(t, doc)
	assertRoundTrip(t, input, doc)
}

func TestParseArray(t *testing.T) {
	input := "ports = [80, 443]\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	assertDocKind(t, doc)
	assertRoundTrip(t, input, doc)
}

func TestParseMixedTypes(t *testing.T) {
	input := "name = \"test\"\ncount = 42\npi = 3.14\nenabled = true\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	assertDocKind(t, doc)
	assertRoundTrip(t, input, doc)
}

func TestParseEmptyDocument(t *testing.T) {
	input := ""
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	assertDocKind(t, doc)
	assertRoundTrip(t, input, doc)
}

func TestParseOnlyComments(t *testing.T) {
	input := "# just a comment\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	assertDocKind(t, doc)
	assertRoundTrip(t, input, doc)
}
