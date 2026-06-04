package cst

// Respelling: rewrite a TOML document into an equivalent valid spelling.
//
// TOML's value/table duality means a single value has several legal textual
// encodings — `[a.b]` header tables vs `b = { ... }` inline tables, dotted keys
// `a.b.c = 1` vs nested `[a.b]`, `[[xs]]` array-of-tables vs `xs = [ {...} ]`
// inline arrays. tommy's encoder emits exactly one canonical spelling per kind,
// so the round-trip fuzzers (which only ever decode encoder output) never test
// whether the decoder ACCEPTS the other spellings. The Respell* functions
// produce those alternative spellings from canonical text so the fuzzer can
// feed them back through the decoder (#107).
//
// They are pure text -> text transforms: parse, move existing leaf nodes into a
// new shape (synthesizing only punctuation nodes), re-serialize via
// Node.Bytes(). No value model is involved — every leaf keeps its already-
// escaped Raw bytes, so multiline strings, sized integers, and quoted keys
// survive untouched. Each function is a no-op (returns input unchanged) when no
// applicable construct is present, so they are safe on any document and compose.
//
// Cycle-1 scope (#107) is deliberately conservative: the transforms fire only
// on the unambiguous, leaf-level cases and leave anything trickier as canonical
// text. A partial transform still yields valid TOML representing the same
// value — it just exercises fewer decoder paths. Deeper nesting and quoted
// dotted-key segments are deferred.

import "bytes"

// inlineSpace is the single-space whitespace node used to pad synthesized
// inline tables/arrays (`{ a = 1 }`, `[ {...} ]`) the way the formatter would.
func inlineSpace() *Node { return &Node{Kind: NodeWhitespace, Raw: []byte(" ")} }

// kvKeyAndValue returns a NodeKeyValue's key node and value node, or nil if the
// node is malformed. The key is the first NodeKey/NodeDottedKey child; the value
// is KeyValueValue.
func kvKeyAndValue(kv *Node) (key, value *Node) {
	for _, c := range kv.Children {
		if c.Kind == NodeKey || c.Kind == NodeDottedKey {
			key = c
			break
		}
	}
	return key, KeyValueValue(kv)
}

// tableBodyKVs returns the NodeKeyValue children of a table (its body), in
// document order.
func tableBodyKVs(table *Node) []*Node {
	var kvs []*Node
	for _, c := range table.Children {
		if c.Kind == NodeKeyValue {
			kvs = append(kvs, c)
		}
	}
	return kvs
}

// tableIsLeaf reports whether a table's body is only key-values (and trivia) —
// it contains no nested structural nodes. Inline tables may only hold key-value
// pairs, so only leaf tables can be inlined in one step.
func tableIsLeaf(table *Node) bool {
	for _, c := range table.Children {
		switch c.Kind {
		case NodeTable, NodeArrayTable:
			return false
		}
	}
	return len(tableBodyKVs(table)) > 0
}

// inlineKV builds a fresh `key = value` NodeKeyValue from an existing kv's key
// and value nodes, padded with single spaces and no trailing newline — the
// shape an inline-table entry takes. The original key/value leaf nodes are
// reused verbatim, preserving their Raw bytes (quoting, escaping, multiline).
func inlineKV(key, value *Node) *Node {
	return &Node{Kind: NodeKeyValue, Children: []*Node{
		key,
		inlineSpace(),
		{Kind: NodeEquals, Raw: []byte("=")},
		inlineSpace(),
		value,
	}}
}

// buildInlineTable assembles a `{ k = v, k2 = v2 }` NodeInlineTable from a
// table's body key-values. Returns nil if there are no key-values.
func buildInlineTable(kvs []*Node) *Node {
	if len(kvs) == 0 {
		return nil
	}
	children := []*Node{{Kind: NodeBraceOpen, Raw: []byte("{")}, inlineSpace()}
	for i, kv := range kvs {
		key, value := kvKeyAndValue(kv)
		if key == nil || value == nil {
			return nil
		}
		if i > 0 {
			children = append(children, &Node{Kind: NodeComma, Raw: []byte(",")}, inlineSpace())
		}
		children = append(children, inlineKV(key, value))
	}
	children = append(children, inlineSpace(), &Node{Kind: NodeBraceClose, Raw: []byte("}")})
	return &Node{Kind: NodeInlineTable, Children: children}
}

// topLevelKV builds a `key = value` NodeKeyValue ready to sit at a document or
// table-body position: a single QuoteKey-ed key node, value, trailing newline.
// key is one segment — to emit a dotted key (a.b.c), use dottedKeyKV.
func topLevelKV(key string, value *Node) *Node {
	return &Node{Kind: NodeKeyValue, Children: []*Node{
		{Kind: NodeKey, Raw: []byte(QuoteKey(key))},
		inlineSpace(),
		{Kind: NodeEquals, Raw: []byte("=")},
		inlineSpace(),
		value,
		{Kind: NodeNewline, Raw: []byte("\n")},
	}}
}

// dottedKeyKV builds a `seg0.seg1.… = value` NodeKeyValue whose key is a
// NodeDottedKey of per-segment NodeKey/NodeDot children (the parser's dotted-key
// shape), each segment QuoteKey-ed via headerKeyNodes. A single segment degrades
// to topLevelKV.
func dottedKeyKV(segs []string, value *Node) *Node {
	if len(segs) == 1 {
		return topLevelKV(segs[0], value)
	}
	return &Node{Kind: NodeKeyValue, Children: []*Node{
		{Kind: NodeDottedKey, Children: headerKeyNodes(segs)},
		inlineSpace(),
		{Kind: NodeEquals, Raw: []byte("=")},
		inlineSpace(),
		value,
		{Kind: NodeNewline, Raw: []byte("\n")},
	}}
}

// tableHasPrefixChild reports whether any table/array-table in nodes (other than
// table itself) has a header whose segments begin with table's segments — i.e.
// table is a super-table completed by a deeper one. Such a table cannot be
// inlined in one step because it owns sub-tables.
func tableHasPrefixChild(nodes []*Node, table *Node) bool {
	segs := TableHeaderSegments(table)
	for _, c := range nodes {
		if c == table {
			continue
		}
		if c.Kind != NodeTable && c.Kind != NodeArrayTable {
			continue
		}
		cs := TableHeaderSegments(c)
		if len(cs) > len(segs) && segmentsHavePrefix(cs, segs) {
			return true
		}
	}
	return false
}

// findTableBySegments returns the first NodeTable in nodes whose header segments
// equal segs exactly, or nil.
func findTableBySegments(nodes []*Node, segs []string) *Node {
	for _, c := range nodes {
		if c.Kind != NodeTable {
			continue
		}
		cs := TableHeaderSegments(c)
		if len(cs) != len(segs) {
			continue
		}
		if segmentsHavePrefix(cs, segs) {
			return c
		}
	}
	return nil
}

// RespellInlineTables rewrites leaf header tables into inline-table key-values,
// the inline-spelling dual of [a.b] sub-tables.
//
//   - A single-segment leaf table `[env]\nk = "v"` becomes `env = { k = "v" }`
//     at the document root, in the table's position.
//   - A two-segment leaf table `[direnv.dotenv]\nk = "v"` whose parent table
//     `[direnv]` is present becomes `dotenv = { k = "v" }` appended into the
//     parent's body, and the `[direnv.dotenv]` header is removed.
//
// Tables that own sub-tables (non-leaf), or whose parent header is absent (e.g.
// a top-level map[string]struct's `[actions.build]` with no `[actions]`), or
// that are deeper than two segments, are left as canonical text. This is a
// no-op when nothing qualifies.
func RespellInlineTables(toml []byte) ([]byte, error) {
	root, err := Parse(toml)
	if err != nil {
		return nil, err
	}
	orig := root.Children
	var out []*Node
	for _, node := range orig {
		if node.Kind != NodeTable || !tableIsLeaf(node) || tableHasPrefixChild(orig, node) {
			out = append(out, node)
			continue
		}
		segs := TableHeaderSegments(node)
		inline := buildInlineTable(tableBodyKVs(node))
		if inline == nil {
			out = append(out, node)
			continue
		}
		switch len(segs) {
		case 1:
			// Replace the [env] table with `env = { ... }` at root.
			out = append(out, topLevelKV(segs[0], inline))
		case 2:
			parent := findTableBySegments(orig, segs[:1])
			if parent == nil {
				out = append(out, node) // parent header absent: leave canonical
				continue
			}
			// Append `dotenv = { ... }` into the parent table's body, drop header.
			parent.Children = append(parent.Children, topLevelKV(segs[1], inline))
		default:
			out = append(out, node) // deeper than two segments: deferred
		}
	}
	root.Children = out
	return root.Bytes(), nil
}

// RespellDottedKeys rewrites a single-segment leaf table into top-level
// dotted-key assignments: `[a]\nname = "x"\nport = 1` becomes
// `a.name = "x"\na.port = 1`. It is the dual of a nested struct expressed as a
// header table.
//
// Cycle-1 scope: fires only on single-segment leaf tables with bare-key headers
// and bare-key bodies, and only when no other table shares the `a` prefix (TOML
// forbids redefining a table, so `a.name = ...` alongside `[a.b]` would be
// illegal). Anything else is left canonical. No-op when nothing qualifies.
func RespellDottedKeys(toml []byte) ([]byte, error) {
	root, err := Parse(toml)
	if err != nil {
		return nil, err
	}
	orig := root.Children
	var out []*Node
	for _, node := range orig {
		if node.Kind != NodeTable || !tableIsLeaf(node) || tableHasPrefixChild(orig, node) {
			out = append(out, node)
			continue
		}
		segs := TableHeaderSegments(node)
		if len(segs) != 1 || KeyNeedsQuoting(segs[0]) {
			out = append(out, node)
			continue
		}
		prefix := segs[0]
		kvs := tableBodyKVs(node)
		ok := true
		for _, kv := range kvs {
			key, _ := kvKeyAndValue(kv)
			if key == nil || key.Kind != NodeKey || KeyNeedsQuoting(StripQuotes(string(key.Raw))) {
				ok = false
				break
			}
		}
		if !ok {
			out = append(out, node)
			continue
		}
		for _, kv := range kvs {
			key, value := kvKeyAndValue(kv)
			leaf := StripQuotes(string(key.Raw))
			out = append(out, dottedKeyKV([]string{prefix, leaf}, value))
		}
	}
	root.Children = out
	return root.Bytes(), nil
}

// buildInlineArray assembles a `[ {...}, {...} ]` NodeArray of inline tables from
// per-entry key-value lists. Returns nil if any entry has no key-values.
func buildInlineArray(entries [][]*Node) *Node {
	children := []*Node{{Kind: NodeBracketOpen, Raw: []byte("[")}, inlineSpace()}
	for i, kvs := range entries {
		inline := buildInlineTable(kvs)
		if inline == nil {
			return nil
		}
		if i > 0 {
			children = append(children, &Node{Kind: NodeComma, Raw: []byte(",")}, inlineSpace())
		}
		children = append(children, inline)
	}
	children = append(children, inlineSpace(), &Node{Kind: NodeBracketClose, Raw: []byte("]")})
	return &Node{Kind: NodeArray, Children: children}
}

// RespellInlineArrays rewrites a run of single-segment leaf array-of-tables
// entries into one inline array of inline tables: `[[xs]]\nh = "a"\n[[xs]]\nh =
// "b"` becomes `xs = [ { h = "a" }, { h = "b" } ]`.
//
// Cycle-1 scope: fires only on single-segment array-tables whose entries are
// all leaves (scalar-only bodies, no nested sub-tables/array-tables). Entries
// with nested structure, or multi-segment headers, are left canonical. No-op
// when nothing qualifies.
func RespellInlineArrays(toml []byte) ([]byte, error) {
	root, err := Parse(toml)
	if err != nil {
		return nil, err
	}
	orig := root.Children

	// Group consecutive [[key]] entries by single-segment header, requiring every
	// entry to be a leaf. A key disqualifies if any of its entries is non-leaf.
	type group struct {
		key     string
		entries [][]*Node
		ok      bool
	}
	var order []string
	groups := map[string]*group{}
	for _, node := range orig {
		if node.Kind != NodeArrayTable {
			continue
		}
		segs := TableHeaderSegments(node)
		if len(segs) != 1 || KeyNeedsQuoting(segs[0]) {
			continue
		}
		g, seen := groups[segs[0]]
		if !seen {
			g = &group{key: segs[0], ok: true}
			groups[segs[0]] = g
			order = append(order, segs[0])
		}
		if !tableIsLeaf(node) || tableHasPrefixChild(orig, node) {
			g.ok = false
			continue
		}
		g.entries = append(g.entries, tableBodyKVs(node))
	}

	// A group is rewritable only if every same-keyed array-table is a qualifying
	// leaf single-segment entry. Build the inline-array KV for each such group.
	inlineFor := map[string]*Node{}
	for _, k := range order {
		g := groups[k]
		if !g.ok || len(g.entries) == 0 {
			continue
		}
		arr := buildInlineArray(g.entries)
		if arr == nil {
			continue
		}
		inlineFor[k] = topLevelKV(k, arr)
	}
	if len(inlineFor) == 0 {
		return root.Bytes(), nil
	}

	// Emit: replace the FIRST [[key]] entry of each rewritable group with the
	// inline-array KV, drop the rest.
	emitted := map[string]bool{}
	var out []*Node
	for _, node := range orig {
		if node.Kind == NodeArrayTable {
			segs := TableHeaderSegments(node)
			if len(segs) == 1 {
				if kv, rw := inlineFor[segs[0]]; rw {
					if !emitted[segs[0]] {
						out = append(out, kv)
						emitted[segs[0]] = true
					}
					continue
				}
			}
		}
		out = append(out, node)
	}
	root.Children = out
	return root.Bytes(), nil
}

// respellNoChange reports whether respelling produced byte-identical output —
// used by tests to assert a transform was a no-op on inapplicable input.
func respellNoChange(in, out []byte) bool { return bytes.Equal(in, out) }
