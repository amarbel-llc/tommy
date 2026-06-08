package cst

import (
	"fmt"
	"strings"
)

// Decompose collapses a parsed CST into a canonical VALUE model — the
// normalization layer of the 2026-06-07 ADR. TOML's value/table duality gives
// one value many surface spellings (header vs inline table, dotted key vs nested
// table, array-of-tables vs inline array, implicit vs explicit parent); a
// type-driven decoder that pattern-matches the CST must enumerate them per kind,
// which is combinatorial and where decode bugs hide. Decompose resolves every
// spelling ONCE into a single shape, so a decoder folding the type algebra over
// the result has exactly one reader per kind and no spelling fallbacks.
//
// The model is intentionally thin: a Table is an ordered list of named Values; a
// Value is a scalar Leaf (carrying its source key-value node so the existing
// Extract* / TOMLUnmarshaler / TextUnmarshaler paths are reused verbatim), a
// Table, or an Array of tables. Scalar arrays ([]int, []string) stay Leaves —
// they are extracted as a unit. Present-but-empty (#21) is preserved (an empty
// table/array is a present Value; absence is a missing field).
//
// Duplicate-key detection is intrinsic and total: defining the same key twice in
// any scope, redefining a header table, or colliding a leaf with a table is an
// error in EVERY spelling at once (subsuming the scattered #90/#92/#102/#110
// guards).

// ValueKind discriminates the value model node.
type ValueKind int

const (
	VLeaf  ValueKind = iota // scalar, scalar array, or custom/text leaf
	VTable                  // a table: ordered named values
	VArray                  // an array of tables ([[x]] or inline xs = [ {…} ])
)

// Value is a node of the canonical value model.
type Value struct {
	Kind   ValueKind
	Leaf   *Node   // VLeaf: the source NodeKeyValue (Extract* operate on it)
	Fields []Field // VTable: in first-seen order
	Items  []Value // VArray: entries, each Kind==VTable
	Node   *Node   // source structural node (header table / array-table entry /
	//   inline table), for round-trip-stable encode handles; nil at the root

	explicit bool // VTable: defined directly by a header (for #92 dup detection)
	seen     bool // the decoder recognized this value's key (descend in Undecoded)
	full     bool // this value AND its whole subtree are accounted for (leaf / map)
}

// Field is one entry of a VTable.
type Field struct {
	Key string
	Val Value
}

// Get returns the field value for key and whether it is present. VTable only.
func (v *Value) Get(key string) (*Value, bool) {
	for i := range v.Fields {
		if v.Fields[i].Key == key {
			return &v.Fields[i].Val, true
		}
	}
	return nil, false
}

// GetPath navigates a dotted key-path from this table (e.g. "server.tls"),
// returning the value at the path and whether it is present. Each segment
// descends one VTable level; a missing segment or a non-table encountered
// mid-path yields (nil, false). It is the ergonomic way to reach a nested value
// or whole subtree to MarkSeen/MarkConsumed before calling Undecoded. Segments
// are split on "." (bare-key paths), matching the dotted paths Undecoded reports.
func (v *Value) GetPath(dotted string) (*Value, bool) {
	cur := v
	for _, seg := range strings.Split(dotted, ".") {
		next, ok := cur.Get(seg)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// MarkSeen records that the decoder recognized this value's key. For a struct
// table or array it means "entered" — Undecoded still descends to surface any
// unconsumed child (an unknown struct field). Generated decoders call it.
func (v *Value) MarkSeen() { v.seen = true }

// MarkConsumed records that this value AND its entire subtree are accounted for
// — a scalar leaf the decoder read, or a map field that absorbs every entry.
// Undecoded does not descend a consumed value. Generated decoders call it.
func (v *Value) MarkConsumed() { v.seen = true; v.full = true }

// Undecoded returns the dotted key-paths present in the model that no decoder
// consumed — the spelling-independent replacement for document.UndecodedKeys.
// The receiver is treated as entered (its fields are walked); a field whose
// value was never seen is reported (without descending), a seen struct/array is
// descended, and a consumed value contributes nothing.
func (v *Value) Undecoded() []string {
	var out []string
	v.collectUndecoded("", &out)
	return out
}

func (v *Value) collectUndecoded(prefix string, out *[]string) {
	if v.full {
		return
	}
	switch v.Kind {
	case VTable:
		for i := range v.Fields {
			f := &v.Fields[i]
			p := joinPath(prefix, []string{f.Key})
			if !f.Val.seen {
				*out = append(*out, p)
			} else {
				f.Val.collectUndecoded(p, out)
			}
		}
	case VArray:
		for i := range v.Items {
			it := &v.Items[i]
			if it.seen {
				it.collectUndecoded(fmt.Sprintf("%s[%d]", prefix, i), out)
			}
		}
	}
}

// DecomposeBytes parses TOML and returns the canonical value model in one call,
// equivalent to Decompose(Parse(data)). It is the entry point for computing
// undecoded ("unknown") keys WITHOUT decoding through a generated decoder or
// reflection marshal: parse, mark the keys you recognize via Get/GetPath +
// MarkConsumed/MarkSeen, then call Undecoded. Because the model normalizes every
// TOML spelling first (inline vs header table, dotted vs nested key), this is
// spelling-correct where a raw-CST key walk — the retired document.UndecodedKeys
// — was not.
func DecomposeBytes(data []byte) (*Value, error) {
	root, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return Decompose(root)
}

// Decompose builds the canonical value model from a parsed document root.
func Decompose(root *Node) (*Value, error) {
	t := &Value{Kind: VTable, explicit: true}
	for _, c := range root.Children {
		switch c.Kind {
		case NodeKeyValue:
			if err := insertKV(t, keyValueSegments(c), c, ""); err != nil {
				return nil, err
			}
		case NodeTable:
			segs := TableHeaderSegments(c)
			target, err := ensureTable(t, segs, "", true)
			if err != nil {
				return nil, err
			}
			target.Node = c
			if err := mergeBody(target, c, dotted(segs)); err != nil {
				return nil, err
			}
		case NodeArrayTable:
			segs := TableHeaderSegments(c)
			entry, err := appendArrayEntry(t, segs, "")
			if err != nil {
				return nil, err
			}
			entry.Node = c
			if err := mergeBody(entry, c, dotted(segs)); err != nil {
				return nil, err
			}
		}
	}
	return t, nil
}

// mergeBody inserts a container's key-value children (a table or inline-table
// body) into table t. Bodies hold only key-values (tables are flat at the
// document root), each possibly dotted. prefix is t's path, for error messages.
func mergeBody(t *Value, container *Node, prefix string) error {
	for _, c := range container.Children {
		if c.Kind != NodeKeyValue {
			continue
		}
		if err := insertKV(t, keyValueSegments(c), c, prefix); err != nil {
			return err
		}
	}
	return nil
}

// insertKV sets the value of key-value kv at the dotted path segs within table
// t, creating implicit intermediate tables. The value is decomposed: an inline
// table becomes a VTable, an inline array of inline tables a VArray, anything
// else (scalar, scalar array) a VLeaf carrying kv.
func insertKV(t *Value, segs []string, kv *Node, prefix string) error {
	if len(segs) == 0 {
		return fmt.Errorf("malformed key-value")
	}
	parent, err := ensureTable(t, segs[:len(segs)-1], prefix, false)
	if err != nil {
		return err
	}
	leaf := segs[len(segs)-1]
	if _, dup := parent.Get(leaf); dup {
		return fmt.Errorf("duplicate key %q", joinPath(prefix, segs))
	}
	val, err := decomposeValue(kv, joinPath(prefix, segs))
	if err != nil {
		return err
	}
	parent.Fields = append(parent.Fields, Field{Key: leaf, Val: val})
	return nil
}

// ensureTable navigates/creates the table at path segs within t. Intermediate
// tables are created implicitly. explicit marks the FINAL table as
// header-defined: redefining an already-explicit table is invalid TOML (#92);
// colliding with a non-table key is an error in any case.
func ensureTable(t *Value, segs []string, prefix string, explicit bool) (*Value, error) {
	cur := t
	for i, seg := range segs {
		child, ok := cur.Get(seg)
		if !ok {
			cur.Fields = append(cur.Fields, Field{Key: seg, Val: Value{Kind: VTable}})
			child = &cur.Fields[len(cur.Fields)-1].Val
		} else if child.Kind == VArray {
			// A dotted/sub-table path into an array-of-tables addresses its LAST
			// entry (the one currently being built), matching TOML's semantics.
			if len(child.Items) == 0 {
				return nil, fmt.Errorf("key %q is an array, not a table", dotted(segs[:i+1]))
			}
			child = &child.Items[len(child.Items)-1]
		} else if child.Kind != VTable {
			return nil, fmt.Errorf("key %q is not a table", dotted(segs[:i+1]))
		}
		cur = child
	}
	if explicit {
		if cur.explicit {
			return nil, fmt.Errorf("duplicate table %q", dotted(segs))
		}
		cur.explicit = true
	}
	return cur, nil
}

// appendArrayEntry ensures the array-of-tables at path segs within t and appends
// a fresh entry table, returning it. The intermediate path is created
// implicitly; the leaf must be absent or already a VArray.
func appendArrayEntry(t *Value, segs []string, prefix string) (*Value, error) {
	parent, err := ensureTable(t, segs[:len(segs)-1], prefix, false)
	if err != nil {
		return nil, err
	}
	leaf := segs[len(segs)-1]
	arr, ok := parent.Get(leaf)
	if !ok {
		parent.Fields = append(parent.Fields, Field{Key: leaf, Val: Value{Kind: VArray}})
		arr = &parent.Fields[len(parent.Fields)-1].Val
	} else if arr.Kind != VArray {
		return nil, fmt.Errorf("key %q is not an array of tables", dotted(segs))
	}
	arr.Items = append(arr.Items, Value{Kind: VTable, explicit: true})
	return &arr.Items[len(arr.Items)-1], nil
}

// decomposeValue turns a key-value's value into a model Value: an inline table
// recurses to a VTable, an inline array whose elements are all inline tables to a
// VArray, and anything else (scalar, scalar array) stays a VLeaf carrying kv so
// the existing typed extractors read it.
func decomposeValue(kv *Node, path string) (Value, error) {
	v := KeyValueValue(kv)
	if v == nil {
		return Value{}, fmt.Errorf("%s: malformed value", path)
	}
	switch v.Kind {
	case NodeInlineTable:
		t := Value{Kind: VTable, explicit: true, Node: v}
		if err := mergeBody(&t, v, path); err != nil {
			return Value{}, err
		}
		return t, nil
	case NodeArray:
		if entries, ok := inlineTableElements(v); ok {
			arr := Value{Kind: VArray}
			for _, e := range entries {
				entry := Value{Kind: VTable, explicit: true, Node: e}
				if err := mergeBody(&entry, e, path+"[]"); err != nil {
					return Value{}, err
				}
				arr.Items = append(arr.Items, entry)
			}
			return arr, nil
		}
	}
	return Value{Kind: VLeaf, Leaf: kv}, nil
}

// inlineTableElements returns the inline-table elements of an array and true iff
// the array is a non-empty array of ONLY inline tables (ignoring punctuation /
// trivia) — the inline spelling of an array-of-tables. A scalar element (or an
// empty array) yields false, so scalar arrays stay leaves.
func inlineTableElements(arr *Node) ([]*Node, bool) {
	var tables []*Node
	for _, c := range arr.Children {
		switch {
		case c.Kind == NodeInlineTable:
			tables = append(tables, c)
		case isArrayTrivia(c.Kind):
			// structural punctuation / trivia
		default:
			return nil, false // a scalar (or nested array) element: not array-of-tables
		}
	}
	return tables, len(tables) > 0
}

// isArrayTrivia reports whether a NodeArray child is structural punctuation or
// trivia (brackets, separators, whitespace, comments) rather than a value
// element. The array-spelling readers — inlineTableElements, IsEmptyArray, and
// the Extract*Slice extractors — share this one classification.
func isArrayTrivia(k NodeKind) bool {
	switch k {
	case NodeWhitespace, NodeNewline, NodeComment, NodeComma, NodeBracketOpen, NodeBracketClose:
		return true
	default:
		return false
	}
}

// IsEmptyArray reports whether v is a leaf carrying an empty inline array
// (`key = []`). Decompose keeps an empty array a VLeaf because emptiness erases
// the array-of-tables-vs-scalar-array distinction (#94): there are no inline
// tables to inspect, so inlineTableElements declines it. A struct-slice decoder
// consults this to treat `xs = []` as an empty array-of-tables (an empty slice)
// rather than a type error, matching how an empty scalar array already decodes
// to an empty []int.
func (v *Value) IsEmptyArray() bool {
	if v.Kind != VLeaf || v.Leaf == nil {
		return false
	}
	av := KeyValueValue(v.Leaf)
	if av == nil || av.Kind != NodeArray {
		return false
	}
	for _, c := range av.Children {
		if !isArrayTrivia(c.Kind) {
			return false // a real element (scalar or inline table)
		}
	}
	return true
}

func dotted(segs []string) string { return strings.Join(segs, ".") }
func joinPath(prefix string, segs []string) string {
	j := dotted(segs)
	if prefix == "" {
		return j
	}
	return prefix + "." + j
}
