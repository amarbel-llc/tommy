package cst

import (
	"github.com/amarbel-llc/tommy/internal/lexer"
)

type parser struct {
	tokens []lexer.Token
	pos    int
}

// Parse consumes raw TOML input and returns a CST document node.
// Every token becomes a leaf node; structural nodes group their children.
// Concatenating all leaf Raw bytes reproduces the original input byte-for-byte.
func Parse(input []byte) (*Node, error) {
	tokens := lexer.Lex(input)
	p := &parser{tokens: tokens}
	return p.parseDocument(), nil
}

func (p *parser) peek() (lexer.Token, bool) {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos], true
	}
	return lexer.Token{}, false
}

func (p *parser) advance() lexer.Token {
	tok := p.tokens[p.pos]
	p.pos++
	return tok
}

func (p *parser) parseDocument() *Node {
	doc := &Node{Kind: NodeDocument}

	for p.pos < len(p.tokens) {
		tok, _ := p.peek()
		switch tok.Kind {
		case lexer.TokenBracketOpen:
			doc.Children = append(doc.Children, p.parseTable())
		case lexer.TokenDoubleBracketOpen:
			doc.Children = append(doc.Children, p.parseArrayTable())
		case lexer.TokenWhitespace, lexer.TokenNewline, lexer.TokenComment:
			doc.Children = append(doc.Children, p.leafFromToken(p.advance()))
		default:
			// Must be a key-value at the document top level
			doc.Children = append(doc.Children, p.parseKeyValue())
		}
	}

	return doc
}

func (p *parser) parseTable() *Node {
	table := &Node{Kind: NodeTable}

	// Consume [ key... ] and trailing trivia/newline on the header line
	table.Children = append(table.Children, p.leafFromToken(p.advance())) // [
	p.consumeKeyTokensInto(table)                                         // key parts
	// Expect ]
	if tok, ok := p.peek(); ok && tok.Kind == lexer.TokenBracketClose {
		table.Children = append(table.Children, p.leafFromToken(p.advance()))
	}

	// Consume trailing trivia and newline on header line
	p.consumeTrailingTriviaInto(table)

	// Consume body: key-values and trivia until next table header or EOF
	p.consumeTableBodyInto(table)

	return table
}

func (p *parser) parseArrayTable() *Node {
	table := &Node{Kind: NodeArrayTable}

	// [[ is a single token (TokenDoubleBracketOpen) — emit as two NodeBracketOpen
	openTok := p.advance()
	table.Children = append(table.Children,
		&Node{Kind: NodeBracketOpen, Raw: openTok.Raw[:1]},
		&Node{Kind: NodeBracketOpen, Raw: openTok.Raw[1:2]},
	)

	p.consumeKeyTokensInto(table)

	// Expect ]]
	if tok, ok := p.peek(); ok && tok.Kind == lexer.TokenDoubleBracketClose {
		closeTok := p.advance()
		table.Children = append(table.Children,
			&Node{Kind: NodeBracketClose, Raw: closeTok.Raw[:1]},
			&Node{Kind: NodeBracketClose, Raw: closeTok.Raw[1:2]},
		)
	}

	p.consumeTrailingTriviaInto(table)
	p.consumeTableBodyInto(table)

	return table
}

func (p *parser) consumeTableBodyInto(parent *Node) {
	for p.pos < len(p.tokens) {
		tok, _ := p.peek()
		switch tok.Kind {
		case lexer.TokenBracketOpen, lexer.TokenDoubleBracketOpen:
			return // next table header
		case lexer.TokenWhitespace, lexer.TokenNewline, lexer.TokenComment:
			parent.Children = append(parent.Children, p.leafFromToken(p.advance()))
		default:
			parent.Children = append(parent.Children, p.parseKeyValue())
		}
	}
}

func (p *parser) parseKeyValue() *Node {
	kv := &Node{Kind: NodeKeyValue}

	// Parse key (possibly dotted)
	kv.Children = append(kv.Children, p.parseKey())

	// Consume whitespace before =
	p.consumeWhitespaceInto(kv)

	// Expect =
	if tok, ok := p.peek(); ok && tok.Kind == lexer.TokenEquals {
		kv.Children = append(kv.Children, p.leafFromToken(p.advance()))
	}

	// Consume whitespace after =
	p.consumeWhitespaceInto(kv)

	// Parse value
	kv.Children = append(kv.Children, p.parseValue())

	// Consume trailing trivia (whitespace, comments) and newline
	p.consumeTrailingTriviaInto(kv)

	return kv
}

func (p *parser) parseKey() *Node {
	// Collect key tokens: could be bare key, quoted string, or dotted key
	// First key part
	firstKey := p.consumeOneKeyPart()

	// Check if dotted
	if tok, ok := p.peek(); ok && tok.Kind == lexer.TokenDot {
		dotted := &Node{Kind: NodeDottedKey}
		dotted.Children = append(dotted.Children, firstKey)

		for {
			tok, ok := p.peek()
			if !ok || tok.Kind != lexer.TokenDot {
				break
			}
			dotted.Children = append(dotted.Children, p.leafFromToken(p.advance())) // .
			// Consume whitespace around dot
			p.consumeWhitespaceInto(dotted)
			dotted.Children = append(dotted.Children, p.consumeOneKeyPart())
		}
		return dotted
	}

	return firstKey
}

func (p *parser) consumeOneKeyPart() *Node {
	tok := p.advance()
	switch tok.Kind {
	case lexer.TokenBareKey:
		return &Node{Kind: NodeKey, Raw: tok.Raw}
	case lexer.TokenBasicString, lexer.TokenLiteralString:
		return &Node{Kind: NodeKey, Raw: tok.Raw}
	default:
		// Fallback: treat as key
		return &Node{Kind: NodeKey, Raw: tok.Raw}
	}
}

func (p *parser) parseValue() *Node {
	tok, ok := p.peek()
	if !ok {
		return &Node{Kind: NodeString}
	}

	switch tok.Kind {
	case lexer.TokenBasicString, lexer.TokenMultilineBasicString,
		lexer.TokenLiteralString, lexer.TokenMultilineLiteralString:
		t := p.advance()
		return &Node{Kind: NodeString, Raw: t.Raw}
	case lexer.TokenInteger:
		t := p.advance()
		return &Node{Kind: NodeInteger, Raw: t.Raw}
	case lexer.TokenFloat:
		t := p.advance()
		return &Node{Kind: NodeFloat, Raw: t.Raw}
	case lexer.TokenBool:
		t := p.advance()
		return &Node{Kind: NodeBool, Raw: t.Raw}
	case lexer.TokenDateTime:
		t := p.advance()
		return &Node{Kind: NodeDateTime, Raw: t.Raw}
	case lexer.TokenBracketOpen:
		return p.parseArray()
	case lexer.TokenBraceOpen:
		return p.parseInlineTable()
	default:
		// Consume unknown as string fallback
		t := p.advance()
		return &Node{Kind: NodeString, Raw: t.Raw}
	}
}

func (p *parser) parseArray() *Node {
	arr := &Node{Kind: NodeArray}

	// [
	arr.Children = append(arr.Children, p.leafFromToken(p.advance()))

	for p.pos < len(p.tokens) {
		tok, ok := p.peek()
		if !ok {
			break
		}

		switch tok.Kind {
		case lexer.TokenBracketClose:
			arr.Children = append(arr.Children, p.leafFromToken(p.advance()))
			return arr
		case lexer.TokenComma:
			arr.Children = append(arr.Children, p.leafFromToken(p.advance()))
		case lexer.TokenWhitespace, lexer.TokenNewline, lexer.TokenComment:
			arr.Children = append(arr.Children, p.leafFromToken(p.advance()))
		default:
			arr.Children = append(arr.Children, p.parseValue())
		}
	}

	return arr
}

func (p *parser) parseInlineTable() *Node {
	tbl := &Node{Kind: NodeInlineTable}

	// {
	tbl.Children = append(tbl.Children, p.leafFromToken(p.advance()))

	for p.pos < len(p.tokens) {
		tok, ok := p.peek()
		if !ok {
			break
		}

		switch tok.Kind {
		case lexer.TokenBraceClose:
			tbl.Children = append(tbl.Children, p.leafFromToken(p.advance()))
			return tbl
		case lexer.TokenComma:
			tbl.Children = append(tbl.Children, p.leafFromToken(p.advance()))
		case lexer.TokenWhitespace, lexer.TokenNewline:
			tbl.Children = append(tbl.Children, p.leafFromToken(p.advance()))
		default:
			// inline key-value (without trailing newline handling — inline tables
			// don't have newlines between entries in TOML v1.0)
			tbl.Children = append(tbl.Children, p.parseInlineKeyValue())
		}
	}

	return tbl
}

func (p *parser) parseInlineKeyValue() *Node {
	kv := &Node{Kind: NodeKeyValue}

	kv.Children = append(kv.Children, p.parseKey())
	p.consumeWhitespaceInto(kv)

	if tok, ok := p.peek(); ok && tok.Kind == lexer.TokenEquals {
		kv.Children = append(kv.Children, p.leafFromToken(p.advance()))
	}

	p.consumeWhitespaceInto(kv)
	kv.Children = append(kv.Children, p.parseValue())

	return kv
}

// consumeKeyTokensInto consumes key tokens (bare keys, strings, dots, whitespace)
// inside a table header [ ... ] or [[ ... ]].
func (p *parser) consumeKeyTokensInto(parent *Node) {
	for p.pos < len(p.tokens) {
		tok, ok := p.peek()
		if !ok {
			break
		}
		switch tok.Kind {
		case lexer.TokenBareKey, lexer.TokenBasicString, lexer.TokenLiteralString:
			parent.Children = append(parent.Children, &Node{Kind: NodeKey, Raw: tok.Raw})
			p.pos++
		case lexer.TokenDot:
			parent.Children = append(parent.Children, p.leafFromToken(p.advance()))
		case lexer.TokenWhitespace:
			parent.Children = append(parent.Children, p.leafFromToken(p.advance()))
		default:
			return
		}
	}
}

func (p *parser) consumeWhitespaceInto(parent *Node) {
	for p.pos < len(p.tokens) {
		tok, ok := p.peek()
		if !ok || tok.Kind != lexer.TokenWhitespace {
			break
		}
		parent.Children = append(parent.Children, p.leafFromToken(p.advance()))
	}
}

func (p *parser) consumeTrailingTriviaInto(parent *Node) {
	for p.pos < len(p.tokens) {
		tok, ok := p.peek()
		if !ok {
			break
		}
		switch tok.Kind {
		case lexer.TokenWhitespace, lexer.TokenComment:
			parent.Children = append(parent.Children, p.leafFromToken(p.advance()))
		case lexer.TokenNewline:
			parent.Children = append(parent.Children, p.leafFromToken(p.advance()))
			return
		default:
			return
		}
	}
}

func (p *parser) leafFromToken(tok lexer.Token) *Node {
	return &Node{
		Kind: tokenToNodeKind(tok.Kind),
		Raw:  tok.Raw,
	}
}

func tokenToNodeKind(tk lexer.TokenKind) NodeKind {
	switch tk {
	case lexer.TokenEquals:
		return NodeEquals
	case lexer.TokenDot:
		return NodeDot
	case lexer.TokenComma:
		return NodeComma
	case lexer.TokenBracketOpen:
		return NodeBracketOpen
	case lexer.TokenBracketClose:
		return NodeBracketClose
	case lexer.TokenBraceOpen:
		return NodeBraceOpen
	case lexer.TokenBraceClose:
		return NodeBraceClose
	case lexer.TokenComment:
		return NodeComment
	case lexer.TokenWhitespace:
		return NodeWhitespace
	case lexer.TokenNewline:
		return NodeNewline
	default:
		return NodeKey
	}
}
