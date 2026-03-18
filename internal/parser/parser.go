package parser

import (
	"github.com/amarbel-llc/tommy/internal/lexer"
	"github.com/amarbel-llc/tommy/pkg/cst"
)

type parser struct {
	tokens []lexer.Token
	pos    int
}

// Parse consumes raw TOML input and returns a CST document node.
// Every token becomes a leaf node; structural nodes group their children.
// Concatenating all leaf Raw bytes reproduces the original input byte-for-byte.
func Parse(input []byte) (*cst.Node, error) {
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

func (p *parser) parseDocument() *cst.Node {
	doc := &cst.Node{Kind: cst.NodeDocument}

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

func (p *parser) parseTable() *cst.Node {
	table := &cst.Node{Kind: cst.NodeTable}

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

func (p *parser) parseArrayTable() *cst.Node {
	table := &cst.Node{Kind: cst.NodeArrayTable}

	// [[ is a single token (TokenDoubleBracketOpen) — emit as two NodeBracketOpen
	openTok := p.advance()
	table.Children = append(table.Children,
		&cst.Node{Kind: cst.NodeBracketOpen, Raw: openTok.Raw[:1]},
		&cst.Node{Kind: cst.NodeBracketOpen, Raw: openTok.Raw[1:2]},
	)

	p.consumeKeyTokensInto(table)

	// Expect ]]
	if tok, ok := p.peek(); ok && tok.Kind == lexer.TokenDoubleBracketClose {
		closeTok := p.advance()
		table.Children = append(table.Children,
			&cst.Node{Kind: cst.NodeBracketClose, Raw: closeTok.Raw[:1]},
			&cst.Node{Kind: cst.NodeBracketClose, Raw: closeTok.Raw[1:2]},
		)
	}

	p.consumeTrailingTriviaInto(table)
	p.consumeTableBodyInto(table)

	return table
}

func (p *parser) consumeTableBodyInto(parent *cst.Node) {
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

func (p *parser) parseKeyValue() *cst.Node {
	kv := &cst.Node{Kind: cst.NodeKeyValue}

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

func (p *parser) parseKey() *cst.Node {
	// Collect key tokens: could be bare key, quoted string, or dotted key
	// First key part
	firstKey := p.consumeOneKeyPart()

	// Check if dotted
	if tok, ok := p.peek(); ok && tok.Kind == lexer.TokenDot {
		dotted := &cst.Node{Kind: cst.NodeDottedKey}
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

func (p *parser) consumeOneKeyPart() *cst.Node {
	tok := p.advance()
	switch tok.Kind {
	case lexer.TokenBareKey:
		return &cst.Node{Kind: cst.NodeKey, Raw: tok.Raw}
	case lexer.TokenBasicString, lexer.TokenLiteralString:
		return &cst.Node{Kind: cst.NodeKey, Raw: tok.Raw}
	default:
		// Fallback: treat as key
		return &cst.Node{Kind: cst.NodeKey, Raw: tok.Raw}
	}
}

func (p *parser) parseValue() *cst.Node {
	tok, ok := p.peek()
	if !ok {
		return &cst.Node{Kind: cst.NodeString}
	}

	switch tok.Kind {
	case lexer.TokenBasicString, lexer.TokenMultilineBasicString,
		lexer.TokenLiteralString, lexer.TokenMultilineLiteralString:
		t := p.advance()
		return &cst.Node{Kind: cst.NodeString, Raw: t.Raw}
	case lexer.TokenInteger:
		t := p.advance()
		return &cst.Node{Kind: cst.NodeInteger, Raw: t.Raw}
	case lexer.TokenFloat:
		t := p.advance()
		return &cst.Node{Kind: cst.NodeFloat, Raw: t.Raw}
	case lexer.TokenBool:
		t := p.advance()
		return &cst.Node{Kind: cst.NodeBool, Raw: t.Raw}
	case lexer.TokenDateTime:
		t := p.advance()
		return &cst.Node{Kind: cst.NodeDateTime, Raw: t.Raw}
	case lexer.TokenBracketOpen:
		return p.parseArray()
	case lexer.TokenBraceOpen:
		return p.parseInlineTable()
	default:
		// Consume unknown as string fallback
		t := p.advance()
		return &cst.Node{Kind: cst.NodeString, Raw: t.Raw}
	}
}

func (p *parser) parseArray() *cst.Node {
	arr := &cst.Node{Kind: cst.NodeArray}

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

func (p *parser) parseInlineTable() *cst.Node {
	tbl := &cst.Node{Kind: cst.NodeInlineTable}

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

func (p *parser) parseInlineKeyValue() *cst.Node {
	kv := &cst.Node{Kind: cst.NodeKeyValue}

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
func (p *parser) consumeKeyTokensInto(parent *cst.Node) {
	for p.pos < len(p.tokens) {
		tok, ok := p.peek()
		if !ok {
			break
		}
		switch tok.Kind {
		case lexer.TokenBareKey, lexer.TokenBasicString, lexer.TokenLiteralString:
			parent.Children = append(parent.Children, &cst.Node{Kind: cst.NodeKey, Raw: tok.Raw})
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

func (p *parser) consumeWhitespaceInto(parent *cst.Node) {
	for p.pos < len(p.tokens) {
		tok, ok := p.peek()
		if !ok || tok.Kind != lexer.TokenWhitespace {
			break
		}
		parent.Children = append(parent.Children, p.leafFromToken(p.advance()))
	}
}

func (p *parser) consumeTrailingTriviaInto(parent *cst.Node) {
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

func (p *parser) leafFromToken(tok lexer.Token) *cst.Node {
	return &cst.Node{
		Kind: tokenToNodeKind(tok.Kind),
		Raw:  tok.Raw,
	}
}

func tokenToNodeKind(tk lexer.TokenKind) cst.NodeKind {
	switch tk {
	case lexer.TokenEquals:
		return cst.NodeEquals
	case lexer.TokenDot:
		return cst.NodeDot
	case lexer.TokenComma:
		return cst.NodeComma
	case lexer.TokenBracketOpen:
		return cst.NodeBracketOpen
	case lexer.TokenBracketClose:
		return cst.NodeBracketClose
	case lexer.TokenBraceOpen:
		return cst.NodeBraceOpen
	case lexer.TokenBraceClose:
		return cst.NodeBraceClose
	case lexer.TokenComment:
		return cst.NodeComment
	case lexer.TokenWhitespace:
		return cst.NodeWhitespace
	case lexer.TokenNewline:
		return cst.NodeNewline
	default:
		return cst.NodeKey
	}
}
