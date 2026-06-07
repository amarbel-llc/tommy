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

	// Synthetic marks a node fabricated by an accessor rather than produced by
	// the parser or an encode build. It is set only by FindImplicitChildTable on
	// the detached implicit-parent node it returns (#113/#116); implicitScope
	// keys its ChildScope fallback off this flag rather than recognizing the
	// node's shape structurally, so a future Node-building site cannot
	// accidentally divert real nodes into the implicit path. A synthetic node is
	// never part of the document tree — it must not be mutated or serialized
	// (Bytes ignores the flag, concatenating Raw/Children as usual).
	Synthetic bool
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
