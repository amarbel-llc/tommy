package cst

import (
	"github.com/amarbel-llc/tommy/internal/lexer"
)

// parserOld is the original slice-based parser, preserved for benchmarking
// against the iterator-based parser.
type parserOld struct {
	tokens []lexer.Token
	pos    int
}

func ParseOld(input []byte) *Node {
	tokens := lexer.Lex(input)
	p := &parserOld{tokens: tokens}
	return p.parseDocument()
}

func (p *parserOld) peek() (lexer.Token, bool) {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos], true
	}
	return lexer.Token{}, false
}

func (p *parserOld) advance() lexer.Token {
	tok := p.tokens[p.pos]
	p.pos++
	return tok
}

func (p *parserOld) parseDocument() *Node {
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
			doc.Children = append(doc.Children, p.parseKeyValue())
		}
	}
	return doc
}

func (p *parserOld) parseTable() *Node {
	table := &Node{Kind: NodeTable}
	table.Children = append(table.Children, p.leafFromToken(p.advance()))
	p.consumeKeyTokensInto(table)
	if tok, ok := p.peek(); ok && tok.Kind == lexer.TokenBracketClose {
		table.Children = append(table.Children, p.leafFromToken(p.advance()))
	}
	p.consumeTrailingTriviaInto(table)
	p.consumeTableBodyInto(table)
	return table
}

func (p *parserOld) parseArrayTable() *Node {
	table := &Node{Kind: NodeArrayTable}
	openTok := p.advance()
	table.Children = append(table.Children,
		&Node{Kind: NodeBracketOpen, Raw: openTok.Raw[:1]},
		&Node{Kind: NodeBracketOpen, Raw: openTok.Raw[1:2]},
	)
	p.consumeKeyTokensInto(table)
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

func (p *parserOld) consumeTableBodyInto(parent *Node) {
	for p.pos < len(p.tokens) {
		tok, _ := p.peek()
		switch tok.Kind {
		case lexer.TokenBracketOpen, lexer.TokenDoubleBracketOpen:
			return
		case lexer.TokenWhitespace, lexer.TokenNewline, lexer.TokenComment:
			parent.Children = append(parent.Children, p.leafFromToken(p.advance()))
		default:
			parent.Children = append(parent.Children, p.parseKeyValue())
		}
	}
}

func (p *parserOld) parseKeyValue() *Node {
	kv := &Node{Kind: NodeKeyValue}
	kv.Children = append(kv.Children, p.parseKey())
	p.consumeWhitespaceInto(kv)
	if tok, ok := p.peek(); ok && tok.Kind == lexer.TokenEquals {
		kv.Children = append(kv.Children, p.leafFromToken(p.advance()))
	}
	p.consumeWhitespaceInto(kv)
	kv.Children = append(kv.Children, p.parseValue())
	p.consumeTrailingTriviaInto(kv)
	return kv
}

func (p *parserOld) parseKey() *Node {
	firstKey := p.consumeOneKeyPart()
	if tok, ok := p.peek(); ok && tok.Kind == lexer.TokenDot {
		dotted := &Node{Kind: NodeDottedKey}
		dotted.Children = append(dotted.Children, firstKey)
		for {
			tok, ok := p.peek()
			if !ok || tok.Kind != lexer.TokenDot {
				break
			}
			dotted.Children = append(dotted.Children, p.leafFromToken(p.advance()))
			p.consumeWhitespaceInto(dotted)
			dotted.Children = append(dotted.Children, p.consumeOneKeyPart())
		}
		return dotted
	}
	return firstKey
}

func (p *parserOld) consumeOneKeyPart() *Node {
	tok := p.advance()
	return &Node{Kind: NodeKey, Raw: tok.Raw}
}

func (p *parserOld) parseValue() *Node {
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
		t := p.advance()
		return &Node{Kind: NodeString, Raw: t.Raw}
	}
}

func (p *parserOld) parseArray() *Node {
	arr := &Node{Kind: NodeArray}
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

func (p *parserOld) parseInlineTable() *Node {
	tbl := &Node{Kind: NodeInlineTable}
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
			tbl.Children = append(tbl.Children, p.parseInlineKeyValue())
		}
	}
	return tbl
}

func (p *parserOld) parseInlineKeyValue() *Node {
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

func (p *parserOld) consumeKeyTokensInto(parent *Node) {
	for p.pos < len(p.tokens) {
		tok, ok := p.peek()
		if !ok {
			break
		}
		switch tok.Kind {
		case lexer.TokenBareKey, lexer.TokenBasicString, lexer.TokenLiteralString:
			t := p.advance()
			parent.Children = append(parent.Children, &Node{Kind: NodeKey, Raw: t.Raw})
		case lexer.TokenDot:
			parent.Children = append(parent.Children, p.leafFromToken(p.advance()))
		case lexer.TokenWhitespace:
			parent.Children = append(parent.Children, p.leafFromToken(p.advance()))
		default:
			return
		}
	}
}

func (p *parserOld) consumeWhitespaceInto(parent *Node) {
	for p.pos < len(p.tokens) {
		tok, ok := p.peek()
		if !ok || tok.Kind != lexer.TokenWhitespace {
			break
		}
		parent.Children = append(parent.Children, p.leafFromToken(p.advance()))
	}
}

func (p *parserOld) consumeTrailingTriviaInto(parent *Node) {
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

func (p *parserOld) leafFromToken(tok lexer.Token) *Node {
	return &Node{
		Kind: tokenToNodeKind(tok.Kind),
		Raw:  tok.Raw,
	}
}
