package formatter

import (
	"testing"
)

func TestNormalizeEqualsWhitespace(t *testing.T) {
	input := []byte("key   =   \"value\"\n")
	expected := "key = \"value\"\n"
	got := string(Format(input))
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestPreservesComments(t *testing.T) {
	input := []byte("# important comment\nkey = \"value\"\n")
	got := string(Format(input))
	if got != string(input) {
		t.Errorf("got %q, want %q", got, string(input))
	}
}

func TestTrailingWhitespaceRemoved(t *testing.T) {
	input := []byte("key = \"value\"   \n")
	expected := "key = \"value\"\n"
	got := string(Format(input))
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestBlankLinesBetweenTables(t *testing.T) {
	input := []byte("[a]\nkey = 1\n[b]\nkey = 2\n")
	expected := "[a]\nkey = 1\n\n[b]\nkey = 2\n"
	got := string(Format(input))
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestTrailingNewlineNormalized(t *testing.T) {
	input := []byte("key = 1\n\n\n")
	expected := "key = 1\n"
	got := string(Format(input))
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestFormatIdempotent(t *testing.T) {
	inputs := []string{
		"key = \"value\"\n",
		"# comment\nkey = 1\n",
		"[a]\nkey = 1\n\n[b]\nkey = 2\n",
		"key   =   \"value\"   \n",
		"[a]\nkey = 1\n[b]\nkey = 2\n",
		"key = 1\n\n\n",
	}

	for _, input := range inputs {
		first := Format([]byte(input))
		second := Format(first)
		if string(first) != string(second) {
			t.Errorf("not idempotent for input %q:\n  first:  %q\n  second: %q", input, string(first), string(second))
		}
	}
}

func TestTrailingCommentWhitespace(t *testing.T) {
	input := []byte("key = \"val\"   # comment   \n")
	expected := "key = \"val\" # comment\n"
	got := string(Format(input))
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestMultipleBlankLinesBetweenTablesCollapsed(t *testing.T) {
	input := []byte("[a]\nkey = 1\n\n\n\n[b]\nkey = 2\n")
	expected := "[a]\nkey = 1\n\n[b]\nkey = 2\n"
	got := string(Format(input))
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestNoTablesPreservesContent(t *testing.T) {
	input := []byte("key = \"value\"\n")
	got := string(Format(input))
	if got != string(input) {
		t.Errorf("got %q, want %q", got, string(input))
	}
}

func TestTableWithCommentInBody(t *testing.T) {
	input := []byte("[server]\n# port setting\nport = 8080\n")
	got := string(Format(input))
	if got != string(input) {
		t.Errorf("got %q, want %q", got, string(input))
	}
}
