package cst

type NodeKind int

const (
	NodeDocument   NodeKind = iota
	NodeTable               // [table]
	NodeArrayTable          // [[array-of-tables]]
	NodeKeyValue            // key = value
	NodeKey                 // bare or quoted key
	NodeDottedKey           // a.b.c
	NodeEquals              // =

	// Values
	NodeString
	NodeInteger
	NodeFloat
	NodeBool
	NodeDateTime
	NodeArray       // [1, 2, 3]
	NodeInlineTable // {a = 1, b = 2}

	// Trivia
	NodeComment    // # ...
	NodeWhitespace // spaces, tabs
	NodeNewline    // \n, \r\n

	// Punctuation
	NodeBracketOpen  // [
	NodeBracketClose // ]
	NodeBraceOpen    // {
	NodeBraceClose   // }
	NodeComma        // ,
	NodeDot          // .
)

type Node struct {
	Kind     NodeKind
	Raw      []byte
	Children []*Node
}

func (n *Node) Bytes() []byte {
	if len(n.Children) == 0 {
		return n.Raw
	}
	var out []byte
	for _, child := range n.Children {
		out = append(out, child.Bytes()...)
	}
	return out
}
