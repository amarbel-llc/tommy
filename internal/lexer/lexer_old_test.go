package lexer

import "bytes"

// lexerOld is the original []byte-based lexer, preserved for benchmarking.
type lexerOld struct {
	input  []byte
	pos    int
	state  lexerState
	tokens []Token
	bracketDepth int
	braceDepth   int
}

func LexOld(input []byte) []Token {
	l := &lexerOld{input: input, state: stateLineStart}
	l.lex()
	return l.tokens
}

func (l *lexerOld) lex() {
	for l.pos < len(l.input) {
		prevPos := l.pos
		prevState := l.state
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
		if l.pos == prevPos && l.state == prevState {
			start := l.pos
			l.pos++
			l.emit(TokenInvalid, start)
		}
	}
}

func (l *lexerOld) peek() byte {
	if l.pos < len(l.input) {
		return l.input[l.pos]
	}
	return 0
}

func (l *lexerOld) peekAt(offset int) byte {
	i := l.pos + offset
	if i < len(l.input) {
		return l.input[i]
	}
	return 0
}

func (l *lexerOld) emit(kind TokenKind, start int) {
	l.tokens = append(l.tokens, Token{Kind: kind, Raw: l.input[start:l.pos]})
}

func (l *lexerOld) lexLineStart() {
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

func (l *lexerOld) lexKey() {
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
	case ch == '}':
		l.state = stateValue
	default:
		l.consumeBareKey()
	}
}

func (l *lexerOld) lexAfterEquals() {
	ch := l.peek()
	switch {
	case ch == ' ' || ch == '\t':
		l.consumeWhitespace()
	default:
		l.state = stateValue
	}
}

func (l *lexerOld) lexValue() {
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
		if l.braceDepth > 0 && l.bracketDepth == 0 {
			l.state = stateKey
		}
	case ch == '=':
		start := l.pos
		l.pos++
		l.emit(TokenEquals, start)
		l.state = stateAfterEquals
	case isLetter(ch) || ch == '_':
		if l.braceDepth > 0 && !l.lookingAtBool() {
			l.consumeBareKey()
		} else {
			l.consumeValueWord()
		}
	default:
		l.consumeNumberOrDateTime()
	}
}

func (l *lexerOld) lookingAtBool() bool {
	rest := l.input[l.pos:]
	return bytes.HasPrefix(rest, []byte("true")) && !isBareKeyChar(l.peekAt(4)) ||
		bytes.HasPrefix(rest, []byte("false")) && !isBareKeyChar(l.peekAt(5))
}

func (l *lexerOld) consumeWhitespace() {
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

func (l *lexerOld) consumeComment() {
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

func (l *lexerOld) consumeBareKey() {
	start := l.pos
	for l.pos < len(l.input) && isBareKeyChar(l.input[l.pos]) {
		l.pos++
	}
	if l.pos > start {
		l.emit(TokenBareKey, start)
	}
}

func (l *lexerOld) consumeBasicString() {
	start := l.pos
	if l.peekAt(1) == '"' && l.peekAt(2) == '"' {
		l.pos += 3
		for l.pos < len(l.input) {
			if l.input[l.pos] == '"' && l.peekAt(1) == '"' && l.peekAt(2) == '"' {
				l.pos += 3
				for l.pos < len(l.input) && l.input[l.pos] == '"' {
					l.pos++
				}
				break
			}
			if l.input[l.pos] == '\\' {
				l.pos++
			}
			l.pos++
		}
		l.emit(TokenMultilineBasicString, start)
	} else {
		l.pos++
		for l.pos < len(l.input) {
			ch := l.input[l.pos]
			if ch == '\\' {
				l.pos += 2
				continue
			}
			if ch == '"' {
				l.pos++
				break
			}
			l.pos++
		}
		l.emit(TokenBasicString, start)
	}
}

func (l *lexerOld) consumeLiteralString() {
	start := l.pos
	if l.peekAt(1) == '\'' && l.peekAt(2) == '\'' {
		l.pos += 3
		for l.pos < len(l.input) {
			if l.input[l.pos] == '\'' && l.peekAt(1) == '\'' && l.peekAt(2) == '\'' {
				l.pos += 3
				for l.pos < len(l.input) && l.input[l.pos] == '\'' {
					l.pos++
				}
				break
			}
			l.pos++
		}
		l.emit(TokenMultilineLiteralString, start)
	} else {
		l.pos++
		for l.pos < len(l.input) {
			if l.input[l.pos] == '\'' {
				l.pos++
				break
			}
			l.pos++
		}
		l.emit(TokenLiteralString, start)
	}
}

func (l *lexerOld) consumeValueWord() {
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
	l.consumeBareKey()
}

func (l *lexerOld) consumeNumberOrDateTime() {
	start := l.pos
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
