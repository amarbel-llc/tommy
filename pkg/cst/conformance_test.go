package cst

import (
	"bytes"
	"testing"
)

func TestConformanceRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		// Basic types
		{"bare key", "key = \"value\"\n"},
		{"integer", "int = 42\n"},
		{"negative integer", "int = -17\n"},
		{"hex integer", "hex = 0xDEADBEEF\n"},
		{"octal integer", "oct = 0o755\n"},
		{"binary integer", "bin = 0b11010110\n"},
		{"float", "flt = 3.14\n"},
		{"negative float", "flt = -0.01\n"},
		{"exponent", "flt = 5e+22\n"},
		{"inf", "val = inf\n"},
		{"negative inf", "val = -inf\n"},
		{"nan", "val = nan\n"},
		{"bool true", "val = true\n"},
		{"bool false", "val = false\n"},

		// Strings
		{"basic string", "str = \"hello world\"\n"},
		{"escape sequences", "str = \"tab\\there\"\n"},
		{"unicode escape", "str = \"\\u00E9\"\n"},
		{"multiline basic", "str = \"\"\"\nhello\nworld\"\"\"\n"},
		{"literal string", "str = 'no\\escapes'\n"},
		{"multiline literal", "str = '''\nno\\escapes\nhere'''\n"},

		// Dates
		{"offset datetime", "dt = 1979-05-27T07:32:00Z\n"},
		{"local datetime", "dt = 1979-05-27T07:32:00\n"},
		{"local date", "dt = 1979-05-27\n"},
		{"local time", "dt = 07:32:00\n"},

		// Tables
		{"basic table", "[table]\nkey = \"value\"\n"},
		{"nested table", "[a.b.c]\nkey = \"value\"\n"},
		{"multiple tables", "[a]\nk1 = 1\n\n[b]\nk2 = 2\n"},

		// Array of tables
		{"array of tables", "[[products]]\nname = \"Hammer\"\n\n[[products]]\nname = \"Nail\"\n"},

		// Arrays
		{"basic array", "arr = [1, 2, 3]\n"},
		{"mixed type array", "arr = [\"a\", 1, true]\n"},
		{"nested array", "arr = [[1, 2], [3, 4]]\n"},
		{"multiline array", "arr = [\n  1,\n  2,\n  3,\n]\n"},
		{"trailing comma", "arr = [1, 2, 3,]\n"},

		// Inline tables
		{"inline table", "point = {x = 1, y = 2}\n"},
		{"nested inline", "a = {b = {c = 1}}\n"},

		// Dotted keys
		{"dotted key", "a.b = \"value\"\n"},
		{"quoted dotted key", "\"a\".b = \"value\"\n"},

		// Comments
		{"comment only", "# just a comment\n"},
		{"inline comment", "key = \"value\" # comment\n"},
		{"comment before table", "# comment\n[table]\nkey = 1\n"},

		// Whitespace variations
		{"spaces around equals", "key = \"value\"\n"},
		{"tabs around equals", "key\t=\t\"value\"\n"},
		{"blank lines", "a = 1\n\n\nb = 2\n"},

		// Empty
		{"empty document", ""},
		{"only whitespace", "  \n"},
		{"only newlines", "\n\n\n"},

		// Underscores in numbers
		{"integer with underscores", "val = 1_000_000\n"},
		{"hex with underscores", "val = 0xFF_FF\n"},
		{"float with underscores", "val = 1_000.5\n"},

		// Positive prefix
		{"positive integer", "val = +42\n"},
		{"positive float", "val = +1.5\n"},
		{"positive inf", "val = +inf\n"},
		{"positive nan", "val = +nan\n"},

		// Datetime variations
		{"datetime with offset", "dt = 1979-05-27T07:32:00+08:00\n"},
		{"datetime with negative offset", "dt = 1979-05-27T07:32:00-05:00\n"},
		{"datetime with fractional seconds", "dt = 1979-05-27T07:32:00.999999\n"},
		{"datetime space separator", "dt = 1979-05-27 07:32:00\n"},

		// String edge cases
		{"empty basic string", "str = \"\"\n"},
		{"empty literal string", "str = ''\n"},
		{"empty multiline basic", "str = \"\"\"\"\"\"\n"},
		{"empty multiline literal", "str = ''''''\n"},
		{"string with newline escape", "str = \"line1\\nline2\"\n"},
		{"string with backslash", "str = \"C:\\\\path\"\n"},

		// Array edge cases
		{"empty array", "arr = []\n"},
		{"array with comments", "arr = [\n  # comment\n  1,\n  2,\n]\n"},
		{"array with trailing newline", "arr = [\n  1\n]\n"},

		// Inline table edge cases
		{"empty inline table", "tbl = {}\n"},

		// Table with dotted keys inside
		{"table with dotted body keys", "[fruit]\napple.color = \"red\"\n"},

		// Multiple array of tables
		{"multiple array of tables", "[[fruits]]\nname = \"apple\"\n\n[[fruits]]\nname = \"banana\"\n\n[[fruits]]\nname = \"cherry\"\n"},

		// Complex nesting
		{"array of inline tables", "data = [{x = 1}, {x = 2}]\n"},
		{"nested arrays", "val = [[[1]]]\n"},

		// Windows line endings
		{"crlf line ending", "key = \"value\"\r\n"},
		{"crlf in table", "[table]\r\nkey = 1\r\n"},

		// Mixed content
		{"full document", "# Config file\n\ntitle = \"Example\"\n\n[owner]\nname = \"Tom\"\n\n[database]\nserver = \"192.168.1.1\"\nports = [8001, 8001, 8002]\nenabled = true\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := Parse([]byte(tt.input))
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			got := string(doc.Bytes())
			if got != tt.input {
				t.Errorf("round-trip failed:\n  input: %q\n  got:   %q", tt.input, got)
			}
		})
	}
}

func TestConformanceNodeStructure(t *testing.T) {
	t.Run("table produces NodeTable", func(t *testing.T) {
		doc, err := Parse([]byte("[table]\nkey = 1\n"))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, c := range doc.Children {
			if c.Kind == NodeTable {
				found = true
			}
		}
		if !found {
			t.Error("expected NodeTable child")
		}
	})

	t.Run("array table produces NodeArrayTable", func(t *testing.T) {
		doc, err := Parse([]byte("[[items]]\nname = \"a\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, c := range doc.Children {
			if c.Kind == NodeArrayTable {
				found = true
			}
		}
		if !found {
			t.Error("expected NodeArrayTable child")
		}
	})

	t.Run("inline table produces NodeInlineTable", func(t *testing.T) {
		doc, err := Parse([]byte("t = {a = 1}\n"))
		if err != nil {
			t.Fatal(err)
		}
		var kvNode *Node
		for _, c := range doc.Children {
			if c.Kind == NodeKeyValue {
				kvNode = c
			}
		}
		if kvNode == nil {
			t.Fatal("expected NodeKeyValue child")
		}
		found := false
		for _, c := range kvNode.Children {
			if c.Kind == NodeInlineTable {
				found = true
			}
		}
		if !found {
			t.Error("expected NodeInlineTable in key-value children")
		}
	})

	t.Run("array produces NodeArray", func(t *testing.T) {
		doc, err := Parse([]byte("a = [1, 2]\n"))
		if err != nil {
			t.Fatal(err)
		}
		var kvNode *Node
		for _, c := range doc.Children {
			if c.Kind == NodeKeyValue {
				kvNode = c
			}
		}
		if kvNode == nil {
			t.Fatal("expected NodeKeyValue child")
		}
		found := false
		for _, c := range kvNode.Children {
			if c.Kind == NodeArray {
				found = true
			}
		}
		if !found {
			t.Error("expected NodeArray in key-value children")
		}
	})

	t.Run("dotted key produces NodeDottedKey", func(t *testing.T) {
		doc, err := Parse([]byte("a.b = 1\n"))
		if err != nil {
			t.Fatal(err)
		}
		var kvNode *Node
		for _, c := range doc.Children {
			if c.Kind == NodeKeyValue {
				kvNode = c
			}
		}
		if kvNode == nil {
			t.Fatal("expected NodeKeyValue child")
		}
		found := false
		for _, c := range kvNode.Children {
			if c.Kind == NodeDottedKey {
				found = true
			}
		}
		if !found {
			t.Error("expected NodeDottedKey in key-value children")
		}
	})
}

func TestConformanceBytesIdentity(t *testing.T) {
	// Extra paranoia: parse, serialize, re-parse, and verify the second
	// serialization matches the first.
	inputs := []string{
		"# header\n\n[server]\nhost = \"localhost\"\nport = 8080\n\n[[items]]\nname = \"a\"\ntags = [\"x\", \"y\"]\n",
		"a = {b = {c = [1, 2, {d = true}]}}\n",
		"val = 0xDEAD_BEEF\nflt = 1_000.5e+2\ndt = 1979-05-27T07:32:00.999Z\n",
	}

	for _, input := range inputs {
		doc1, err := Parse([]byte(input))
		if err != nil {
			t.Fatalf("first parse failed for %q: %v", input, err)
		}
		out1 := doc1.Bytes()
		if !bytes.Equal(out1, []byte(input)) {
			t.Fatalf("first round-trip failed:\n  input: %q\n  got:   %q", input, string(out1))
		}

		doc2, err := Parse(out1)
		if err != nil {
			t.Fatalf("second parse failed for %q: %v", input, err)
		}
		out2 := doc2.Bytes()
		if !bytes.Equal(out1, out2) {
			t.Errorf("idempotency failed:\n  first:  %q\n  second: %q", string(out1), string(out2))
		}
	}
}
