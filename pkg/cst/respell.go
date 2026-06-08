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
//
// VALUE-preserving, not formatting-preserving: these reshuffle structure to a
// different spelling, so they intentionally do NOT round-trip comments or
// incidental whitespace inside a rewritten table body — a comment on a key-value
// that gets folded into an inline table is dropped. They are a fuzz/equivalence
// tool over the canonical (comment-free) encoder output, not a general
// comment-preserving editing API; reach for the document API for that. A table
// whose body holds a multiline-string value is left canonical (a newline cannot
// appear inside an inline table).

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
// pairs, so only leaf tables can be inlined in one step. An EMPTY table (no
// key-values) is a leaf too: it inlines to `key = {}`. Treating empty tables as
// leaves matters because whether an array-of-tables entry is empty is a runtime
// VALUE property (a zero-valued struct entry) the shape-based fuzzer predicate
// cannot see — so the rewrite must fire deterministically on shape regardless.
func tableIsLeaf(table *Node) bool {
	for _, c := range table.Children {
		switch c.Kind {
		case NodeTable, NodeArrayTable:
			return false
		}
	}
	return true
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

// valueIsInlineSafe reports whether a value node can sit inside an inline table.
// TOML 1.0 forbids newlines inside an inline table, so a multiline string (whose
// Raw bytes carry literal newlines) cannot be inlined — the rewrite must decline
// and leave such a table canonical rather than emit invalid TOML.
func valueIsInlineSafe(value *Node) bool {
	return !bytes.ContainsRune(value.Bytes(), '\n')
}

// buildInlineTable assembles a NodeInlineTable from a table's body key-values:
// `{ k = v, k2 = v2 }`, or `{}` when there are none. Returns nil when the table
// cannot be inlined: a malformed key-value (missing key or value), or a value
// that contains a newline (a multiline string would make the inline table span
// lines, which is invalid TOML). A nil result tells callers to leave the table
// canonical.
func buildInlineTable(kvs []*Node) *Node {
	if len(kvs) == 0 {
		return &Node{Kind: NodeInlineTable, Children: []*Node{
			{Kind: NodeBraceOpen, Raw: []byte("{")},
			{Kind: NodeBraceClose, Raw: []byte("}")},
		}}
	}
	children := []*Node{{Kind: NodeBraceOpen, Raw: []byte("{")}, inlineSpace()}
	for i, kv := range kvs {
		key, value := kvKeyAndValue(kv)
		if key == nil || value == nil || !valueIsInlineSafe(value) {
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

// firstHeaderIndex returns the index of the first NodeTable/NodeArrayTable in
// nodes, or len(nodes) if there is none. A root-level bare key-value is only
// legal TOML before this point — after a table header it binds to that table.
func firstHeaderIndex(nodes []*Node) int {
	for i, c := range nodes {
		if c.Kind == NodeTable || c.Kind == NodeArrayTable {
			return i
		}
	}
	return len(nodes)
}

// deepInlineSubtree folds the table headed by segs — and EVERYTHING under it —
// into one nested inline-table value. It reconstructs the subtree from ALL
// descendant headers (not just an exact [segs.x] child): a map field whose
// entries appear as deeper headers like [a.m.key] with no bare [a.m] header is
// an IMPLICIT intermediate, grouped by its next segment and folded as
// `m = { key = { … } }` — so the rewrite stays value-preserving. The exact
// [segs] table's own leaf key-values (if that header is present) come first,
// then each distinct next-segment child in document order.
//
// Returns nil (decline — caller leaves the whole subtree canonical) when the
// subtree cannot be one inline value: a descendant array-table (`[[..]]` can't
// sit in an inline table — that is RespellInlineArrays' dual) or a leaf value
// carrying a newline (a multiline string, invalid inside `{ }`). An empty
// subtree folds to `{}`. Leaf nodes are reused verbatim (quoting/escaping kept).
func deepInlineSubtree(segs []string, nodes []*Node) *Node {
	var parts []*Node
	if exact := findTableBySegments(nodes, segs); exact != nil {
		parts = append(parts, tableBodyKVs(exact)...)
	}
	var order []string
	seen := map[string]bool{}
	for _, c := range nodes {
		if c.Kind != NodeTable && c.Kind != NodeArrayTable {
			continue
		}
		cs := TableHeaderSegments(c)
		if len(cs) <= len(segs) || !segmentsHavePrefix(cs, segs) {
			continue
		}
		if c.Kind == NodeArrayTable {
			return nil // array-table descendant: not foldable into an inline table
		}
		next := cs[len(segs)]
		if !seen[next] {
			seen[next] = true
			order = append(order, next)
		}
	}
	for _, next := range order {
		child := deepInlineSubtree(append(append([]string{}, segs...), next), nodes)
		if child == nil {
			return nil
		}
		keyNode := &Node{Kind: NodeKey, Raw: []byte(QuoteKey(next))}
		parts = append(parts, inlineKV(keyNode, child))
	}
	return buildInlineTable(parts)
}

// RespellInlineTables rewrites a header-table subtree into a single nested
// inline-table key-value, the inline-spelling dual of `[a.b]` sub-tables. For
// each single-segment table `[a]` it folds `[a]` AND every `[a.…]` sub-table
// under it into one root-level `a = { … }` value (#111): `[env]\nk = "v"`
// becomes `env = { k = "v" }`; `[a]\n[a.b]\nk = "v"` becomes
// `a = { b = { k = "v" } }`; deeper nesting folds the same way. The inlined
// key-value is hoisted ABOVE the first remaining table header (a bare root
// key-value after a `[table]` would bind to that table, not the document), and
// the folded headers are removed.
//
// A subtree is left fully canonical when it cannot become one inline value: it
// contains a descendant array-table, or a leaf carries a multiline string (a
// newline is invalid inside `{ }`). A multi-segment table with no single-segment
// root (e.g. a top-level map[string]struct's `[actions.build]` with no
// `[actions]`) has no leader and is left canonical too. No-op when nothing
// qualifies.
func RespellInlineTables(toml []byte) ([]byte, error) {
	root, err := Parse(toml)
	if err != nil {
		return nil, err
	}
	orig := root.Children
	consumed := map[*Node]bool{}
	var hoist []*Node // inlined root key-values, emitted before any header
	for _, node := range orig {
		if node.Kind != NodeTable || consumed[node] {
			continue
		}
		segs := TableHeaderSegments(node)
		if len(segs) != 1 {
			continue // only a single-segment table roots an inlineable subtree
		}
		inline := deepInlineSubtree(segs, orig)
		if inline == nil {
			continue // decline: leave the whole subtree canonical
		}
		// Consume [a] and every [a.…] sub-table; they are now folded into `inline`.
		for _, t := range orig {
			if t.Kind == NodeTable && segmentsHavePrefix(TableHeaderSegments(t), segs) {
				consumed[t] = true
			}
		}
		hoist = append(hoist, topLevelKV(segs[0], inline))
	}
	if len(hoist) == 0 {
		return root.Bytes(), nil // no-op: nothing qualified
	}
	var out []*Node
	for _, node := range orig {
		if consumed[node] {
			continue
		}
		out = append(out, node)
	}
	at := firstHeaderIndex(out)
	spliced := make([]*Node, 0, len(out)+len(hoist))
	spliced = append(spliced, out[:at]...)
	spliced = append(spliced, hoist...)
	spliced = append(spliced, out[at:]...)
	root.Children = spliced
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
//
// The emitted `a.x = v` dotted key-values are HOISTED above the first remaining
// table header: a root-level key-value after a `[table]` binds to that table,
// so a dotted key for a late-positioned `[a]` would otherwise rebind under a
// preceding header (changing the value). Mirrors RespellInlineTables' hoist.
func RespellDottedKeys(toml []byte) ([]byte, error) {
	root, err := Parse(toml)
	if err != nil {
		return nil, err
	}
	orig := root.Children
	var out, hoist []*Node
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
		if len(kvs) == 0 {
			out = append(out, node) // empty table: no keys to dot, leave canonical
			continue
		}
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
			hoist = append(hoist, dottedKeyKV([]string{prefix, leaf}, value))
		}
	}
	if len(hoist) == 0 {
		return root.Bytes(), nil
	}
	at := firstHeaderIndex(out)
	spliced := make([]*Node, 0, len(out)+len(hoist))
	spliced = append(spliced, out[:at]...)
	spliced = append(spliced, hoist...)
	spliced = append(spliced, out[at:]...)
	root.Children = spliced
	return root.Bytes(), nil
}

// RespellImplicitParents removes a bare `[parent]` header whose body is empty
// AND whose immediately-following header extends it (`[parent.x]` /
// `[[parent.x]]`) — the standalone-dotted spelling (#113/#64/#117). Per the TOML
// spec a dotted header defines its parent tables implicitly, so such a `[parent]`
// is redundant; the encoder never produces this form (it always emits the bare
// parent), so the round-trip fuzzers never reach it.
//
// The "immediately following" test (rather than a document-wide prefix scan) is
// what makes the rewrite VALUE-preserving across array-of-tables: canonical
// output always places a table's sub-tables right after it, so an empty
// `[parent]` whose next header does NOT extend it is the sole (empty) definition
// in its scope — e.g. an empty map entry in one `[[xs]]` entry whose same-keyed
// sibling in ANOTHER entry has deeper children — and must be kept. Only
// empty-bodied parents are removed (one with key-values would lose them); chains
// collapse. A no-op when nothing qualifies.
func RespellImplicitParents(toml []byte) ([]byte, error) {
	root, err := Parse(toml)
	if err != nil {
		return nil, err
	}
	orig := root.Children
	var out []*Node
	for i, node := range orig {
		if node.Kind == NodeTable && len(tableBodyKVs(node)) == 0 && nextHeaderExtends(orig, i) {
			continue // empty parent of a deeper header: defined implicitly, drop it
		}
		out = append(out, node)
	}
	root.Children = out
	return root.Bytes(), nil
}

// nextHeaderExtends reports whether the first table/array-table header AFTER
// nodes[i] has nodes[i]'s header segments as a strict prefix — i.e. nodes[i]'s
// own sub-table immediately follows it (the canonical layout). Scoped by
// construction: a sibling header or a new array-table entry does not extend the
// prefix, so it stops the scan.
func nextHeaderExtends(nodes []*Node, i int) bool {
	segs := TableHeaderSegments(nodes[i])
	for j := i + 1; j < len(nodes); j++ {
		switch nodes[j].Kind {
		case NodeTable, NodeArrayTable:
			cs := TableHeaderSegments(nodes[j])
			return len(cs) > len(segs) && segmentsHavePrefix(cs, segs)
		}
	}
	return false
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

	// Emit: drop every rewritten [[key]] entry, and HOIST the inline-array
	// key-values above the first remaining table header — a root-level
	// `xs = [...]` after a `[table]` would bind to that table, rebinding the
	// array under it (mirrors RespellInlineTables / RespellDottedKeys).
	emitted := map[string]bool{}
	var out, hoist []*Node
	for _, node := range orig {
		if node.Kind == NodeArrayTable {
			segs := TableHeaderSegments(node)
			if len(segs) == 1 {
				if kv, rw := inlineFor[segs[0]]; rw {
					if !emitted[segs[0]] {
						hoist = append(hoist, kv)
						emitted[segs[0]] = true
					}
					continue
				}
			}
		}
		out = append(out, node)
	}
	at := firstHeaderIndex(out)
	spliced := make([]*Node, 0, len(out)+len(hoist))
	spliced = append(spliced, out[:at]...)
	spliced = append(spliced, hoist...)
	spliced = append(spliced, out[at:]...)
	root.Children = spliced
	return root.Bytes(), nil
}
