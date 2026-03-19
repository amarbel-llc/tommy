# Array-of-Tables Support Design

**Issue:** #5 — Support `[[array-of-tables]]` in document and marshal APIs
**Date:** 2026-03-19
**Scope:** Top-level `[[key]]` only. Nested array-of-tables deferred to #6.

## Key-Path Syntax

The key-path parser is extended with an optional index suffix:

| Path | Meaning |
|------|---------|
| `"servers[0].name"` | Field `name` in first `[[servers]]` entry |
| `"servers[2]"` | Entire third `[[servers]]` entry |
| `"servers[].name"` | Append new `[[servers]]`, set `name` in it |
| `"servers"` | Existing behavior for `[servers]` tables, unchanged |

Rules:

- `[N]` requires index N to exist, errors otherwise
- `[]` only valid in `Set`, appends a new entry
- `Delete` on an entry reindexes (slice semantics)

## Architecture: Two-Layer Document API

### Lower Layer — Container Helpers

Exported functions on `*Document` that operate on CST nodes directly:

- `FindArrayTableNodes(key string) []*cst.Node` — returns all `[[key]]` nodes
  in document order. Returns nil if none exist.
- `GetFromContainer[T any](doc, container, key) (T, error)` — reads a value
  from a specific table/array-table node.
- `SetInContainer(container, key, value) error` — sets a key-value within a
  specific node.
- `AppendArrayTableEntry(key) *cst.Node` — adds a new `[[key]]` section after
  the last existing one (or at end of document). Returns the new node.
- `RemoveArrayTableEntry(node) error` — removes a `[[key]]` section.

`GetFromContainer` and `SetInContainer` reuse existing internal
`findValueInContainer` / `setInContainer` logic, exported with an explicit
container node parameter.

### Upper Layer — Key-Path Integration

`Get`, `Set`, and `Delete` are extended to detect `[N]` / `[]` in key paths.

Path resolution flow:

1. Parse key into segments (e.g., `"servers[1].name"` →
   `{key: "servers", index: 1}`, `{key: "name"}`)
2. If first segment has an index, call `FindArrayTableNodes` and index into the
   result
3. Resolve remaining segments within that container using existing logic

Behavior:

- `Get[T]("servers[1].name")` — find second `[[servers]]` node, read `name`
- `Set("servers[1].name", "v")` — find second node, set `name`
- `Set("servers[].name", "v")` — append new entry, set `name` in it
- `Delete("servers[1]")` — remove second node, reindex remaining
- `Delete("servers[1].name")` — remove `name` key from second node

Errors:

- Index out of range → `"index 3 out of range (2 entries)"`
- `[]` used with `Get` or `Delete` → `"append syntax [] only valid in Set"`
- No entries exist → `"no array-of-tables entries for key \"servers\""`

## Marshal API

### Unmarshal

`decodeSliceField` gains a `reflect.Struct` case: find all `[[key]]` nodes via
`FindArrayTableNodes`, allocate a slice of that length, decode each node into a
slice element using `GetFromContainer`.

### Marshal

`encodeSliceField` gains a `reflect.Struct` case:

1. Find existing `[[key]]` nodes
2. For each slice element at index i:
   - If `i < len(nodes)`: update the existing node via `SetInContainer`
   - Otherwise: `AppendArrayTableEntry` and populate the new node
3. Remove trailing nodes if the slice shrank

This preserves comments in existing entries, appends new entries at the end, and
removes deleted entries.

## Rollback

Additive change — no existing behavior replaced. New code paths trigger only
when key paths contain `[N]`/`[]` or struct fields are `[]StructType`. Revert
the commits to remove the feature.
