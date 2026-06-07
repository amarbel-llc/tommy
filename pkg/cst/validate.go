package cst

import "fmt"

// CheckNoDuplicateKeys rejects a key defined more than once within a single
// scope — invalid TOML regardless of how it is spelled ("Defining a key
// multiple times is invalid", "You should not... use the [name] notation more
// than once for the same key"). A scope is the set of key-value pairs sharing a
// container: the document root, each [table] / [[array-table]] body, and each
// inline-table body. The same key in DIFFERENT scopes (e.g. once per
// array-table entry, or under different table headers) is fine and is not
// flagged.
//
// It inspects only NodeKeyValue keys, so it is the general dual of the
// generated decoders' distributed guards: the per-leaf #90 _seen guard (scalar
// leaves), the #92/#102 duplicate-table guards (repeated [table] headers), and
// in particular it closes the inline-table gap (#110) — `mytable = { a = 1 }`
// twice, or a duplicate map/struct field key written inline, which none of
// those localized guards covered. Table HEADERS are left to the #92/#102
// guards: this never reports a repeated [table]/[[array-table]] as a duplicate
// key, so those guards remain the authority (and their messages) for the
// header spelling.
//
// Generated Decode<Name> methods call this once on the parsed document root, so
// every scope (including delegated sub-tables) is validated before decoding.
func CheckNoDuplicateKeys(root *Node) error {
	return checkScopeNoDuplicateKeys(root)
}

// checkScopeNoDuplicateKeys flags a repeated key among container's direct
// key-value children, then recurses into each value's inline-table bodies and
// into nested [table]/[[array-table]] scopes.
func checkScopeNoDuplicateKeys(container *Node) error {
	var seen map[string]bool
	for _, c := range container.Children {
		switch c.Kind {
		case NodeKeyValue:
			name := KeyValueName(c)
			if name != "" {
				if seen[name] {
					return fmt.Errorf("duplicate key %q", name)
				}
				if seen == nil {
					seen = map[string]bool{}
				}
				seen[name] = true
			}
			if err := checkValueNoDuplicateKeys(KeyValueValue(c)); err != nil {
				return err
			}
		case NodeTable, NodeArrayTable:
			// Tables are flat siblings at the document root; each owns its own
			// key-value scope. (Duplicate table HEADERS are the #92/#102 guards'
			// concern, not a duplicate key.)
			if err := checkScopeNoDuplicateKeys(c); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkValueNoDuplicateKeys descends a value node: an inline table is itself a
// scope; an inline array may hold inline-table elements, each its own scope.
func checkValueNoDuplicateKeys(v *Node) error {
	if v == nil {
		return nil
	}
	switch v.Kind {
	case NodeInlineTable:
		return checkScopeNoDuplicateKeys(v)
	case NodeArray:
		for _, e := range v.Children {
			if e.Kind == NodeInlineTable {
				if err := checkScopeNoDuplicateKeys(e); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
