package lexer

import (
	"bytes"
	"testing"
)

func assertRoundTrip(t *testing.T, input string, tokens []Token) {
	t.Helper()
	var buf bytes.Buffer
	for _, tok := range tokens {
		buf.Write(tok.Raw)
	}
	if got := buf.String(); got != input {
		t.Errorf("round-trip failed:\n  input: %q\n  got:   %q", input, got)
	}
}

func assertTokenKinds(t *testing.T, tokens []Token, expected []TokenKind) {
	t.Helper()
	if len(tokens) != len(expected) {
		t.Fatalf("token count mismatch: got %d, want %d\ntokens: %v", len(tokens), len(expected), tokens)
	}
	for i, tok := range tokens {
		if tok.Kind != expected[i] {
			t.Errorf("token[%d] kind = %d, want %d (raw: %q)", i, tok.Kind, expected[i], tok.Raw)
		}
	}
}

func TestLexBareKeyValueString(t *testing.T) {
	input := "key = \"value\"\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenBasicString,
		TokenNewline,
	})
}

func TestLexComment(t *testing.T) {
	input := "# comment\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenComment,
		TokenNewline,
	})
}

func TestLexInteger(t *testing.T) {
	input := "port = 8080\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenInteger,
		TokenNewline,
	})
}

func TestLexFloat(t *testing.T) {
	input := "pi = 3.14\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenFloat,
		TokenNewline,
	})
}

func TestLexBool(t *testing.T) {
	input := "enabled = true\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenBool,
		TokenNewline,
	})
}

func TestLexTable(t *testing.T) {
	input := "[section]\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBracketOpen,
		TokenBareKey,
		TokenBracketClose,
		TokenNewline,
	})
}

func TestLexArrayTable(t *testing.T) {
	input := "[[items]]\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenDoubleBracketOpen,
		TokenBareKey,
		TokenDoubleBracketClose,
		TokenNewline,
	})
}

func TestLexArray(t *testing.T) {
	input := "ports = [80, 443]\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenBracketOpen,
		TokenInteger,
		TokenComma,
		TokenWhitespace,
		TokenInteger,
		TokenBracketClose,
		TokenNewline,
	})
}

func TestLexInlineTable(t *testing.T) {
	input := "point = {x = 1, y = 2}\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenBraceOpen,
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenInteger,
		TokenComma,
		TokenWhitespace,
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenInteger,
		TokenBraceClose,
		TokenNewline,
	})
}

func TestLexMultilineBasicString(t *testing.T) {
	input := "desc = \"\"\"hello\nworld\"\"\"\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenMultilineBasicString,
		TokenNewline,
	})
}

func TestLexLiteralString(t *testing.T) {
	input := "path = 'C:\\Users\\file'\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenLiteralString,
		TokenNewline,
	})
}

func TestLexDottedKey(t *testing.T) {
	input := "a.b.c = \"val\"\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBareKey,
		TokenDot,
		TokenBareKey,
		TokenDot,
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenBasicString,
		TokenNewline,
	})
}

func TestLexQuotedKey(t *testing.T) {
	input := "\"key with spaces\" = \"val\"\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBasicString,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenBasicString,
		TokenNewline,
	})
}

func TestLexDateTime(t *testing.T) {
	input := "created = 2026-03-18T12:00:00Z\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenDateTime,
		TokenNewline,
	})
}

func TestLexBlankLines(t *testing.T) {
	input := "a = 1\n\n\nb = 2\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenInteger,
		TokenNewline,
		TokenNewline,
		TokenNewline,
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenInteger,
		TokenNewline,
	})
}

func TestLexCommentAfterValue(t *testing.T) {
	input := "key = \"val\" # trailing\n"
	tokens := Lex([]byte(input))
	assertRoundTrip(t, input, tokens)
	assertTokenKinds(t, tokens, []TokenKind{
		TokenBareKey,
		TokenWhitespace,
		TokenEquals,
		TokenWhitespace,
		TokenBasicString,
		TokenWhitespace,
		TokenComment,
		TokenNewline,
	})
}
