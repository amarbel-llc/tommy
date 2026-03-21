package lexer

type TokenKind int

const (
	TokenBareKey TokenKind = iota
	TokenBasicString                // "..."
	TokenMultilineBasicString       // """..."""
	TokenLiteralString              // '...'
	TokenMultilineLiteralString     // '''...'''
	TokenInteger
	TokenFloat
	TokenBool
	TokenDateTime
	TokenEquals            // =
	TokenDot               // .
	TokenComma             // ,
	TokenBracketOpen       // [
	TokenBracketClose      // ]
	TokenDoubleBracketOpen // [[
	TokenDoubleBracketClose // ]]
	TokenBraceOpen         // {
	TokenBraceClose        // }
	TokenComment           // # ...
	TokenWhitespace        // spaces/tabs (not newlines)
	TokenNewline           // \n or \r\n
	TokenInvalid           // unrecognized byte
)

type Token struct {
	Kind TokenKind
	Raw  []byte
}
