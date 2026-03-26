package lexer

import (
	"bytes"
	"io"

	"github.com/amarbel-llc/tommy/internal/ringbuf"
)

type lexerState int

const (
	stateLineStart lexerState = iota
	stateKey
	stateAfterEquals
	stateValue
)

type lexer struct {
	rb    *ringbuf.RingBuffer
	state lexerState
	// Track nesting depth for arrays/inline tables to distinguish
	// value-context brackets from table-header brackets.
	bracketDepth int
	braceDepth   int
	// consumed tracks how many bytes into the current token we've scanned.
	// These bytes have not yet been advanced past in the ring buffer.
	consumed int
	// emitted is set to true by emit() and checked by the iterator to detect
	// whether a state handler made progress (since consumed resets to 0
	// after each emit).
	emitted bool
	// window is a cached contiguous view of the readable bytes in the ring
	// buffer. Refreshed after emit() or when peek/peekAt needs more data.
	window []byte
	// arena is a single backing buffer for all token Raw slices. Tokens
	// sub-slice into this buffer instead of allocating per-token.
	arena []byte
	// pending holds the most recently emitted token.
	pending Token
}

func newLexer(r io.Reader) *lexer {
	rb := ringbuf.New(r, 0)
	l := &lexer{rb: rb, state: stateLineStart}
	l.refreshWindow()
	return l
}

// Lex tokenizes raw TOML input into a slice of tokens.
// Concatenating all token Raw bytes reproduces the original input byte-for-byte.
func Lex(input []byte) []Token {
	return LexReader(bytes.NewReader(input))
}

// LexReader tokenizes TOML from an io.Reader into a slice of tokens.
func LexReader(r io.Reader) []Token {
	return Collect(NewTokenIterator(r))
}

// Collect drains a TokenIterator into a slice of tokens.
func Collect(it *TokenIterator) []Token {
	var tokens []Token
	for {
		tok, ok := it.Next()
		if !ok {
			break
		}
		tokens = append(tokens, tok)
	}
	return tokens
}

// TokenIterator produces tokens one at a time from an io.Reader.
type TokenIterator struct {
	l    *lexer
	done bool
}

// NewTokenIterator creates a pull-based token iterator over an io.Reader.
func NewTokenIterator(r io.Reader) *TokenIterator {
	return &TokenIterator{l: newLexer(r)}
}

// NewTokenIteratorFromBytes creates a pull-based token iterator over a []byte.
func NewTokenIteratorFromBytes(input []byte) *TokenIterator {
	return NewTokenIterator(bytes.NewReader(input))
}

// Next returns the next token and true, or a zero Token and false at EOF.
func (it *TokenIterator) Next() (Token, bool) {
	if it.done {
		return Token{}, false
	}
	l := it.l
	for l.hasMore() {
		prevConsumed := l.consumed
		prevState := l.state
		l.emitted = false
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
		if !l.emitted && l.consumed == prevConsumed && l.state == prevState {
			l.consumed++
			l.emit(TokenInvalid)
		}
		if l.emitted {
			return l.pending, true
		}
	}
	it.done = true
	return Token{}, false
}

// refreshWindow updates the cached window from the ring buffer's readable region.
func (l *lexer) refreshWindow() {
	l.window = l.rb.PeekReadable().Bytes()
}

// arenaAlloc copies n bytes from src into the arena and returns a sub-slice.
func (l *lexer) arenaAlloc(src []byte) []byte {
	n := len(src)
	if cap(l.arena)-len(l.arena) < n {
		// Grow: at least double, at least enough for this token.
		newCap := cap(l.arena) * 2
		if newCap < len(l.arena)+n {
			newCap = len(l.arena) + n
		}
		if newCap < 4096 {
			newCap = 4096
		}
		newArena := make([]byte, len(l.arena), newCap)
		copy(newArena, l.arena)
		l.arena = newArena
	}
	start := len(l.arena)
	l.arena = l.arena[:start+n]
	copy(l.arena[start:], src)
	return l.arena[start : start+n]
}

// ensureWindow makes sure at least n bytes are available in the window.
// Returns true if n bytes are available.
func (l *lexer) ensureWindow(n int) bool {
	if n <= len(l.window) {
		return true
	}
	// Try to fill more data from the reader.
	_, err := l.rb.Peek(n)
	if err != nil {
		l.refreshWindow()
		return n <= len(l.window)
	}
	l.refreshWindow()
	return n <= len(l.window)
}

// hasMore returns true if there are unprocessed bytes available.
func (l *lexer) hasMore() bool {
	if l.consumed < len(l.window) {
		return true
	}
	// Window exhausted — try to get more data.
	return l.ensureWindow(l.consumed + 1)
}

// peek returns the byte at the current consumed offset, or 0 if unavailable.
func (l *lexer) peek() byte {
	if l.consumed < len(l.window) {
		return l.window[l.consumed]
	}
	if !l.ensureWindow(l.consumed + 1) {
		return 0
	}
	return l.window[l.consumed]
}

// peekAt returns the byte at consumed+offset, or 0 if unavailable.
func (l *lexer) peekAt(offset int) byte {
	pos := l.consumed + offset
	if pos < len(l.window) {
		return l.window[pos]
	}
	if !l.ensureWindow(pos + 1) {
		return 0
	}
	return l.window[pos]
}

// emit creates a token from the bytes in [0, consumed), copies them into the
// arena, and advances the ring buffer read pointer.
func (l *lexer) emit(kind TokenKind) {
	if l.consumed == 0 {
		return
	}
	raw := l.arenaAlloc(l.window[:l.consumed])
	l.pending = Token{Kind: kind, Raw: raw}
	l.rb.AdvanceRead(l.consumed)
	l.consumed = 0
	l.emitted = true
	l.refreshWindow()
}

func (l *lexer) lexLineStart() {
	ch := l.peek()
	switch {
	case ch == '\n':
		l.consumed++
		l.emit(TokenNewline)
	case ch == '\r' && l.peekAt(1) == '\n':
		l.consumed += 2
		l.emit(TokenNewline)
	case ch == ' ' || ch == '\t':
		l.consumeWhitespace()
	case ch == '#':
		l.consumeComment()
	case ch == '[':
		if l.peekAt(1) == '[' {
			l.consumed += 2
			l.emit(TokenDoubleBracketOpen)
			l.state = stateKey
		} else {
			l.consumed++
			l.emit(TokenBracketOpen)
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
		l.consumed++
		l.emit(TokenNewline)
		l.state = stateLineStart
	case ch == '\r' && l.peekAt(1) == '\n':
		l.consumed += 2
		l.emit(TokenNewline)
		l.state = stateLineStart
	case ch == '#':
		l.consumeComment()
	case ch == '=':
		l.consumed++
		l.emit(TokenEquals)
		l.state = stateAfterEquals
	case ch == '.':
		l.consumed++
		l.emit(TokenDot)
	case ch == '"':
		l.consumeBasicString()
	case ch == '\'':
		l.consumeLiteralString()
	case ch == ']':
		if l.peekAt(1) == ']' {
			l.consumed += 2
			l.emit(TokenDoubleBracketClose)
		} else {
			l.consumed++
			l.emit(TokenBracketClose)
		}
	case ch == '}':
		// Closing brace for inline table — switch back to value state
		// so lexValue can handle it with proper braceDepth tracking.
		l.state = stateValue
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
		l.consumed++
		l.emit(TokenNewline)
		if l.bracketDepth == 0 && l.braceDepth == 0 {
			l.state = stateLineStart
		}
	case ch == '\r' && l.peekAt(1) == '\n':
		l.consumed += 2
		l.emit(TokenNewline)
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
		l.consumed++
		l.bracketDepth++
		l.emit(TokenBracketOpen)
	case ch == ']':
		l.consumed++
		l.bracketDepth--
		l.emit(TokenBracketClose)
		if l.bracketDepth == 0 && l.braceDepth == 0 {
			// Stay in value state for potential trailing comments
		}
	case ch == '{':
		l.consumed++
		l.braceDepth++
		l.emit(TokenBraceOpen)
	case ch == '}':
		l.consumed++
		l.braceDepth--
		l.emit(TokenBraceClose)
	case ch == ',':
		l.consumed++
		l.emit(TokenComma)
		if l.braceDepth > 0 && l.bracketDepth == 0 {
			// Inside inline table (not nested in an array), next thing is a key
			l.state = stateKey
		}
	case ch == '=':
		// Inside inline table
		l.consumed++
		l.emit(TokenEquals)
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
	// Check if we're looking at "true" or "false" followed by a non-bare-key char
	if l.peek() == 't' && l.peekAt(1) == 'r' && l.peekAt(2) == 'u' && l.peekAt(3) == 'e' && !isBareKeyChar(l.peekAt(4)) {
		return true
	}
	if l.peek() == 'f' && l.peekAt(1) == 'a' && l.peekAt(2) == 'l' && l.peekAt(3) == 's' && l.peekAt(4) == 'e' && !isBareKeyChar(l.peekAt(5)) {
		return true
	}
	return false
}

func (l *lexer) consumeWhitespace() {
	for l.hasMore() {
		ch := l.peek()
		if ch != ' ' && ch != '\t' {
			break
		}
		l.consumed++
	}
	l.emit(TokenWhitespace)
}

func (l *lexer) consumeComment() {
	for l.hasMore() {
		ch := l.peek()
		if ch == '\n' || (ch == '\r' && l.peekAt(1) == '\n') {
			break
		}
		l.consumed++
	}
	l.emit(TokenComment)
}

func (l *lexer) consumeBareKey() {
	for l.hasMore() && isBareKeyChar(l.peek()) {
		l.consumed++
	}
	if l.consumed > 0 {
		l.emit(TokenBareKey)
	}
}

func (l *lexer) consumeBasicString() {
	if l.peekAt(1) == '"' && l.peekAt(2) == '"' {
		// Multiline basic string """..."""
		l.consumed += 3
		for l.hasMore() {
			if l.peek() == '"' && l.peekAt(1) == '"' && l.peekAt(2) == '"' {
				// Check for escaped quotes (""""" means content ending with ")
				l.consumed += 3
				// Consume any additional trailing quotes (up to 2 more are valid)
				for l.hasMore() && l.peek() == '"' {
					l.consumed++
				}
				break
			}
			if l.peek() == '\\' {
				l.consumed++ // skip escape char
			}
			l.consumed++
		}
		l.emit(TokenMultilineBasicString)
	} else {
		// Regular basic string "..."
		l.consumed++ // skip opening "
		for l.hasMore() {
			ch := l.peek()
			if ch == '\\' {
				l.consumed += 2 // skip escape sequence
				continue
			}
			if ch == '"' {
				l.consumed++ // skip closing "
				break
			}
			l.consumed++
		}
		l.emit(TokenBasicString)
	}
}

func (l *lexer) consumeLiteralString() {
	if l.peekAt(1) == '\'' && l.peekAt(2) == '\'' {
		// Multiline literal string '''...'''
		l.consumed += 3
		for l.hasMore() {
			if l.peek() == '\'' && l.peekAt(1) == '\'' && l.peekAt(2) == '\'' {
				l.consumed += 3
				// Consume any additional trailing quotes
				for l.hasMore() && l.peek() == '\'' {
					l.consumed++
				}
				break
			}
			l.consumed++
		}
		l.emit(TokenMultilineLiteralString)
	} else {
		// Regular literal string '...'
		l.consumed++ // skip opening '
		for l.hasMore() {
			if l.peek() == '\'' {
				l.consumed++ // skip closing '
				break
			}
			l.consumed++
		}
		l.emit(TokenLiteralString)
	}
}

func (l *lexer) consumeValueWord() {
	if l.peek() == 't' && l.peekAt(1) == 'r' && l.peekAt(2) == 'u' && l.peekAt(3) == 'e' && !isBareKeyChar(l.peekAt(4)) {
		l.consumed += 4
		l.emit(TokenBool)
		return
	}
	if l.peek() == 'f' && l.peekAt(1) == 'a' && l.peekAt(2) == 'l' && l.peekAt(3) == 's' && l.peekAt(4) == 'e' && !isBareKeyChar(l.peekAt(5)) {
		l.consumed += 5
		l.emit(TokenBool)
		return
	}
	if l.peek() == 'i' && l.peekAt(1) == 'n' && l.peekAt(2) == 'f' && !isBareKeyChar(l.peekAt(3)) {
		l.consumed += 3
		l.emit(TokenFloat)
		return
	}
	if l.peek() == 'n' && l.peekAt(1) == 'a' && l.peekAt(2) == 'n' && !isBareKeyChar(l.peekAt(3)) {
		l.consumed += 3
		l.emit(TokenFloat)
		return
	}
	// Fallback: consume as bare key
	l.consumeBareKey()
}

func (l *lexer) consumeNumberOrDateTime() {
	for l.hasMore() {
		ch := l.peek()
		if isValueChar(ch) {
			l.consumed++
		} else {
			break
		}
	}

	kind := classifyNumericValue(l.window[:l.consumed])
	l.emit(kind)
}

func classifyNumericValue(raw []byte) TokenKind {
	if len(raw) == 0 {
		return TokenInteger
	}

	// DateTime patterns: contains T or has date-like pattern (YYYY-MM-DD)
	if len(raw) >= 10 {
		// Check for date pattern: digit-digit-digit-digit-dash
		if isDigit(raw[0]) && isDigit(raw[1]) && isDigit(raw[2]) && isDigit(raw[3]) && raw[4] == '-' {
			return TokenDateTime
		}
	}

	// Check for time-only values (HH:MM:SS)
	if len(raw) >= 5 && isDigit(raw[0]) && isDigit(raw[1]) && raw[2] == ':' {
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
