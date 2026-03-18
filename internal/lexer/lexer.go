package lexer

import (
	"bytes"
)

type lexerState int

const (
	stateLineStart lexerState = iota
	stateKey
	stateAfterEquals
	stateValue
)

type lexer struct {
	input  []byte
	pos    int
	state  lexerState
	tokens []Token
	// Track nesting depth for arrays/inline tables to distinguish
	// value-context brackets from table-header brackets.
	bracketDepth int
	braceDepth   int
}

// Lex tokenizes raw TOML input into a stream of tokens.
// Concatenating all token Raw bytes reproduces the original input byte-for-byte.
func Lex(input []byte) []Token {
	l := &lexer{input: input, state: stateLineStart}
	l.lex()
	return l.tokens
}

func (l *lexer) lex() {
	for l.pos < len(l.input) {
		switch l.state {
		case stateLineStart:
			l.lexLineStart()
		case stateKey:
			l.lexKey()
		case stateAfterEquals:
			l.lexAfterEquals()
		case stateValue:
			l.lexValue()
		}
	}
}

func (l *lexer) peek() byte {
	if l.pos < len(l.input) {
		return l.input[l.pos]
	}
	return 0
}

func (l *lexer) peekAt(offset int) byte {
	i := l.pos + offset
	if i < len(l.input) {
		return l.input[i]
	}
	return 0
}

func (l *lexer) emit(kind TokenKind, start int) {
	l.tokens = append(l.tokens, Token{Kind: kind, Raw: l.input[start:l.pos]})
}

func (l *lexer) lexLineStart() {
	ch := l.peek()
	switch {
	case ch == '\n':
		start := l.pos
		l.pos++
		l.emit(TokenNewline, start)
	case ch == '\r' && l.peekAt(1) == '\n':
		start := l.pos
		l.pos += 2
		l.emit(TokenNewline, start)
	case ch == ' ' || ch == '\t':
		l.consumeWhitespace()
	case ch == '#':
		l.consumeComment()
	case ch == '[':
		if l.peekAt(1) == '[' {
			start := l.pos
			l.pos += 2
			l.emit(TokenDoubleBracketOpen, start)
			l.state = stateKey
		} else {
			start := l.pos
			l.pos++
			l.emit(TokenBracketOpen, start)
			l.state = stateKey
		}
	default:
		l.state = stateKey
	}
}

func (l *lexer) lexKey() {
	ch := l.peek()
	switch {
	case ch == ' ' || ch == '\t':
		l.consumeWhitespace()
	case ch == '\n':
		start := l.pos
		l.pos++
		l.emit(TokenNewline, start)
		l.state = stateLineStart
	case ch == '\r' && l.peekAt(1) == '\n':
		start := l.pos
		l.pos += 2
		l.emit(TokenNewline, start)
		l.state = stateLineStart
	case ch == '#':
		l.consumeComment()
	case ch == '=':
		start := l.pos
		l.pos++
		l.emit(TokenEquals, start)
		l.state = stateAfterEquals
	case ch == '.':
		start := l.pos
		l.pos++
		l.emit(TokenDot, start)
	case ch == '"':
		l.consumeBasicString()
	case ch == '\'':
		l.consumeLiteralString()
	case ch == ']':
		if l.peekAt(1) == ']' {
			start := l.pos
			l.pos += 2
			l.emit(TokenDoubleBracketClose, start)
		} else {
			start := l.pos
			l.pos++
			l.emit(TokenBracketClose, start)
		}
	default:
		l.consumeBareKey()
	}
}

func (l *lexer) lexAfterEquals() {
	ch := l.peek()
	switch {
	case ch == ' ' || ch == '\t':
		l.consumeWhitespace()
	default:
		l.state = stateValue
	}
}

func (l *lexer) lexValue() {
	ch := l.peek()
	switch {
	case ch == ' ' || ch == '\t':
		l.consumeWhitespace()
	case ch == '\n':
		start := l.pos
		l.pos++
		l.emit(TokenNewline, start)
		if l.bracketDepth == 0 && l.braceDepth == 0 {
			l.state = stateLineStart
		}
	case ch == '\r' && l.peekAt(1) == '\n':
		start := l.pos
		l.pos += 2
		l.emit(TokenNewline, start)
		if l.bracketDepth == 0 && l.braceDepth == 0 {
			l.state = stateLineStart
		}
	case ch == '#':
		l.consumeComment()
	case ch == '"':
		l.consumeBasicString()
	case ch == '\'':
		l.consumeLiteralString()
	case ch == '[':
		start := l.pos
		l.pos++
		l.bracketDepth++
		l.emit(TokenBracketOpen, start)
	case ch == ']':
		start := l.pos
		l.pos++
		l.bracketDepth--
		l.emit(TokenBracketClose, start)
		if l.bracketDepth == 0 && l.braceDepth == 0 {
			// Stay in value state for potential trailing comments
		}
	case ch == '{':
		start := l.pos
		l.pos++
		l.braceDepth++
		l.emit(TokenBraceOpen, start)
	case ch == '}':
		start := l.pos
		l.pos++
		l.braceDepth--
		l.emit(TokenBraceClose, start)
	case ch == ',':
		start := l.pos
		l.pos++
		l.emit(TokenComma, start)
		if l.braceDepth > 0 {
			// Inside inline table, next thing is a key
			l.state = stateKey
		}
	case ch == '=':
		// Inside inline table
		start := l.pos
		l.pos++
		l.emit(TokenEquals, start)
		l.state = stateAfterEquals
	case isLetter(ch) || ch == '_':
		// Could be bool, bare key (in inline table), or bare value
		if l.braceDepth > 0 && !l.lookingAtBool() {
			l.consumeBareKey()
		} else {
			l.consumeValueWord()
		}
	default:
		l.consumeNumberOrDateTime()
	}
}

func (l *lexer) lookingAtBool() bool {
	rest := l.input[l.pos:]
	return bytes.HasPrefix(rest, []byte("true")) && !isBareKeyChar(l.peekAt(4)) ||
		bytes.HasPrefix(rest, []byte("false")) && !isBareKeyChar(l.peekAt(5))
}

func (l *lexer) consumeWhitespace() {
	start := l.pos
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch != ' ' && ch != '\t' {
			break
		}
		l.pos++
	}
	l.emit(TokenWhitespace, start)
}

func (l *lexer) consumeComment() {
	start := l.pos
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '\n' || (ch == '\r' && l.peekAt(1) == '\n') {
			break
		}
		l.pos++
	}
	l.emit(TokenComment, start)
}

func (l *lexer) consumeBareKey() {
	start := l.pos
	for l.pos < len(l.input) && isBareKeyChar(l.input[l.pos]) {
		l.pos++
	}
	l.emit(TokenBareKey, start)
}

func (l *lexer) consumeBasicString() {
	start := l.pos
	if l.peekAt(1) == '"' && l.peekAt(2) == '"' {
		// Multiline basic string """..."""
		l.pos += 3
		for l.pos < len(l.input) {
			if l.input[l.pos] == '"' && l.peekAt(1) == '"' && l.peekAt(2) == '"' {
				// Check for escaped quotes (""""" means content ending with ")
				l.pos += 3
				// Consume any additional trailing quotes (up to 2 more are valid)
				for l.pos < len(l.input) && l.input[l.pos] == '"' {
					l.pos++
				}
				break
			}
			if l.input[l.pos] == '\\' {
				l.pos++ // skip escape char
			}
			l.pos++
		}
		l.emit(TokenMultilineBasicString, start)
	} else {
		// Regular basic string "..."
		l.pos++ // skip opening "
		for l.pos < len(l.input) {
			ch := l.input[l.pos]
			if ch == '\\' {
				l.pos += 2 // skip escape sequence
				continue
			}
			if ch == '"' {
				l.pos++ // skip closing "
				break
			}
			l.pos++
		}
		l.emit(TokenBasicString, start)
	}
}

func (l *lexer) consumeLiteralString() {
	start := l.pos
	if l.peekAt(1) == '\'' && l.peekAt(2) == '\'' {
		// Multiline literal string '''...'''
		l.pos += 3
		for l.pos < len(l.input) {
			if l.input[l.pos] == '\'' && l.peekAt(1) == '\'' && l.peekAt(2) == '\'' {
				l.pos += 3
				// Consume any additional trailing quotes
				for l.pos < len(l.input) && l.input[l.pos] == '\'' {
					l.pos++
				}
				break
			}
			l.pos++
		}
		l.emit(TokenMultilineLiteralString, start)
	} else {
		// Regular literal string '...'
		l.pos++ // skip opening '
		for l.pos < len(l.input) {
			if l.input[l.pos] == '\'' {
				l.pos++ // skip closing '
				break
			}
			l.pos++
		}
		l.emit(TokenLiteralString, start)
	}
}

func (l *lexer) consumeValueWord() {
	start := l.pos
	rest := l.input[l.pos:]
	if bytes.HasPrefix(rest, []byte("true")) && !isBareKeyChar(l.peekAt(4)) {
		l.pos += 4
		l.emit(TokenBool, start)
		return
	}
	if bytes.HasPrefix(rest, []byte("false")) && !isBareKeyChar(l.peekAt(5)) {
		l.pos += 5
		l.emit(TokenBool, start)
		return
	}
	if bytes.HasPrefix(rest, []byte("inf")) && !isBareKeyChar(l.peekAt(3)) {
		l.pos += 3
		l.emit(TokenFloat, start)
		return
	}
	if bytes.HasPrefix(rest, []byte("nan")) && !isBareKeyChar(l.peekAt(3)) {
		l.pos += 3
		l.emit(TokenFloat, start)
		return
	}
	// Fallback: consume as bare key
	l.consumeBareKey()
}

func (l *lexer) consumeNumberOrDateTime() {
	start := l.pos

	// Consume all characters that could be part of a number or datetime
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if isValueChar(ch) {
			l.pos++
		} else {
			break
		}
	}

	raw := l.input[start:l.pos]
	kind := classifyNumericValue(raw)
	l.tokens = append(l.tokens, Token{Kind: kind, Raw: raw})
}

func classifyNumericValue(raw []byte) TokenKind {
	if len(raw) == 0 {
		return TokenInteger
	}

	s := string(raw)

	// DateTime patterns: contains T or has date-like pattern (YYYY-MM-DD)
	if len(s) >= 10 {
		// Check for date pattern: digit-digit-digit-digit-dash
		if isDigit(raw[0]) && isDigit(raw[1]) && isDigit(raw[2]) && isDigit(raw[3]) && raw[4] == '-' {
			return TokenDateTime
		}
	}

	// Check for time-only values (HH:MM:SS)
	if len(s) >= 5 && isDigit(raw[0]) && isDigit(raw[1]) && raw[2] == ':' {
		return TokenDateTime
	}

	// Float indicators: contains '.', 'e', 'E' (but not in hex 0x prefix)
	hasHexPrefix := len(raw) > 2 && raw[0] == '0' && (raw[1] == 'x' || raw[1] == 'X')
	if !hasHexPrefix {
		for _, b := range raw {
			if b == '.' || b == 'e' || b == 'E' {
				return TokenFloat
			}
		}
	}

	// Special float values with sign
	if bytes.Equal(raw, []byte("+inf")) || bytes.Equal(raw, []byte("-inf")) ||
		bytes.Equal(raw, []byte("+nan")) || bytes.Equal(raw, []byte("-nan")) {
		return TokenFloat
	}

	return TokenInteger
}

func isValueChar(ch byte) bool {
	// Characters that can appear in numbers, datetimes, and similar unquoted values
	return isDigit(ch) || isLetter(ch) || ch == '-' || ch == '+' ||
		ch == '.' || ch == ':' || ch == '_' || ch == 'T' || ch == 'Z'
}

func isBareKeyChar(ch byte) bool {
	return isLetter(ch) || isDigit(ch) || ch == '-' || ch == '_'
}

func isLetter(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}
