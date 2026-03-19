# Array-of-Tables Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Support `[[array-of-tables]]` in both the document API (Get/Set/Delete with `[N]`/`[]` key paths) and the marshal API (`[]StructType` round-trip).

**Architecture:** Two-layer document API — lower layer provides container helpers (`FindArrayTableNodes`, `GetFromContainer`, `SetInContainer`, `AppendArrayTableEntry`, `RemoveArrayTableEntry`), upper layer extends existing `Get`/`Set`/`Delete` to parse `[N]`/`[]` index syntax in key paths. Marshal API adds `reflect.Struct` handling in `decodeSliceField`/`encodeSliceField` using the lower layer.

**Tech Stack:** Go, existing tommy CST/document/marshal packages.

**Rollback:** Purely additive — revert the commits.

---

### Task 1: FindArrayTableNodes

**Files:**
- Modify: `pkg/document/document.go`
- Test: `pkg/document/document_test.go`

**Step 1: Write the failing test**

Add to `pkg/document/document_test.go`:

```go
func TestFindArrayTableNodes(t *testing.T) {
	input := []byte("[[servers]]\nname = \"a\"\n\n[[servers]]\nname = \"b\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	nodes := doc.FindArrayTableNodes("servers")
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestFindArrayTableNodesEmpty(t *testing.T) {
	input := []byte("name = \"test\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	nodes := doc.FindArrayTableNodes("servers")
	if nodes != nil {
		t.Fatalf("expected nil, got %v", nodes)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/document/ -run TestFindArrayTableNodes -v`
Expected: FAIL — `doc.FindArrayTableNodes undefined`

**Step 3: Write minimal implementation**

Add to `pkg/document/document.go` after `findTableNode` (after line 121):

```go
// FindArrayTableNodes returns all [[key]] CST nodes in document order.
// Returns nil if none exist.
func (doc *Document) FindArrayTableNodes(key string) []*cst.Node {
	var nodes []*cst.Node
	for _, child := range doc.root.Children {
		if child.Kind != cst.NodeArrayTable {
			continue
		}
		if tableHeaderKey(child) == key {
			nodes = append(nodes, child)
		}
	}
	return nodes
}
```

**Step 4: Run test to verify it passes**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/document/ -run TestFindArrayTableNodes -v`
Expected: PASS

**Step 5: Commit**

```
feat(document): add FindArrayTableNodes
```

---

### Task 2: GetFromContainer and SetInContainer

**Files:**
- Modify: `pkg/document/document.go`
- Test: `pkg/document/document_test.go`

**Step 1: Write the failing tests**

Add to `pkg/document/document_test.go`:

```go
func TestGetFromContainer(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\nport = 8080\n\n[[servers]]\nname = \"lux\"\nport = 9090\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	nodes := doc.FindArrayTableNodes("servers")

	name0, err := GetFromContainer[string](doc, nodes[0], "name")
	if err != nil {
		t.Fatal(err)
	}
	if name0 != "grit" {
		t.Fatalf("expected %q, got %q", "grit", name0)
	}

	name1, err := GetFromContainer[string](doc, nodes[1], "name")
	if err != nil {
		t.Fatal(err)
	}
	if name1 != "lux" {
		t.Fatalf("expected %q, got %q", "lux", name1)
	}

	port0, err := GetFromContainer[int](doc, nodes[0], "port")
	if err != nil {
		t.Fatal(err)
	}
	if port0 != 8080 {
		t.Fatalf("expected 8080, got %d", port0)
	}
}

func TestSetInContainer(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\n\n[[servers]]\nname = \"lux\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	nodes := doc.FindArrayTableNodes("servers")

	if err := doc.SetInContainer(nodes[1], "name", "moxy"); err != nil {
		t.Fatal(err)
	}
	expected := "[[servers]]\nname = \"grit\"\n\n[[servers]]\nname = \"moxy\"\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestSetInContainerNewKey(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	nodes := doc.FindArrayTableNodes("servers")

	if err := doc.SetInContainer(nodes[0], "port", 8080); err != nil {
		t.Fatal(err)
	}
	expected := "[[servers]]\nname = \"grit\"\nport = 8080\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/document/ -run "TestGetFromContainer|TestSetInContainer" -v`
Expected: FAIL — undefined functions

**Step 3: Write minimal implementation**

Add to `pkg/document/document.go`:

```go
// GetFromContainer reads a value from a specific table or array-table node.
func GetFromContainer[T any](doc *Document, container *cst.Node, key string) (T, error) {
	var zero T
	valueNode, err := findValueInContainer(container, key)
	if err != nil {
		return zero, err
	}
	result, err := convertNode[T](valueNode)
	if err != nil {
		return zero, fmt.Errorf("key %q: %w", key, err)
	}
	return result, nil
}

// SetInContainer sets a key-value within a specific table or array-table node.
func (doc *Document) SetInContainer(container *cst.Node, key string, value any) error {
	encoded, nodeKind, err := encodeValue(value)
	if err != nil {
		return err
	}
	return setInContainer(container, key, encoded, nodeKind)
}
```

**Step 4: Run tests to verify they pass**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/document/ -run "TestGetFromContainer|TestSetInContainer" -v`
Expected: PASS

**Step 5: Commit**

```
feat(document): add GetFromContainer and SetInContainer
```

---

### Task 3: AppendArrayTableEntry and RemoveArrayTableEntry

**Files:**
- Modify: `pkg/document/document.go`
- Test: `pkg/document/document_test.go`

**Step 1: Write the failing tests**

Add to `pkg/document/document_test.go`:

```go
func TestAppendArrayTableEntry(t *testing.T) {
	input := []byte("# config\n[[servers]]\nname = \"grit\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	newNode := doc.AppendArrayTableEntry("servers")
	if err := doc.SetInContainer(newNode, "name", "lux"); err != nil {
		t.Fatal(err)
	}

	expected := "# config\n[[servers]]\nname = \"grit\"\n\n[[servers]]\nname = \"lux\"\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestAppendArrayTableEntryFirst(t *testing.T) {
	input := []byte("title = \"test\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	newNode := doc.AppendArrayTableEntry("servers")
	if err := doc.SetInContainer(newNode, "name", "grit"); err != nil {
		t.Fatal(err)
	}

	expected := "title = \"test\"\n\n[[servers]]\nname = \"grit\"\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestRemoveArrayTableEntry(t *testing.T) {
	input := []byte("[[servers]]\nname = \"a\"\n\n[[servers]]\nname = \"b\"\n\n[[servers]]\nname = \"c\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	nodes := doc.FindArrayTableNodes("servers")
	if err := doc.RemoveArrayTableEntry(nodes[1]); err != nil {
		t.Fatal(err)
	}

	expected := "[[servers]]\nname = \"a\"\n\n[[servers]]\nname = \"c\"\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestRemoveArrayTableEntryLast(t *testing.T) {
	input := []byte("[[servers]]\nname = \"only\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	nodes := doc.FindArrayTableNodes("servers")
	if err := doc.RemoveArrayTableEntry(nodes[0]); err != nil {
		t.Fatal(err)
	}

	got := string(doc.Bytes())
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/document/ -run "TestAppendArrayTableEntry|TestRemoveArrayTableEntry" -v`
Expected: FAIL — undefined functions

**Step 3: Write minimal implementation**

Add to `pkg/document/document.go`:

```go
// AppendArrayTableEntry adds a new [[key]] section after the last existing
// one, or at the end of the document. Returns the new node.
func (doc *Document) AppendArrayTableEntry(key string) *cst.Node {
	newNode := &cst.Node{
		Kind: cst.NodeArrayTable,
		Children: []*cst.Node{
			{Kind: cst.NodeBracketOpen, Raw: []byte("[")},
			{Kind: cst.NodeBracketOpen, Raw: []byte("[")},
			{Kind: cst.NodeKey, Raw: []byte(key)},
			{Kind: cst.NodeBracketClose, Raw: []byte("]")},
			{Kind: cst.NodeBracketClose, Raw: []byte("]")},
			{Kind: cst.NodeNewline, Raw: []byte("\n")},
		},
	}

	// Find the last [[key]] node to insert after it
	lastIdx := -1
	for i, child := range doc.root.Children {
		if child.Kind == cst.NodeArrayTable && tableHeaderKey(child) == key {
			lastIdx = i
		}
	}

	// Also include any children that belong to the last array-table entry
	// (key-values, trivia that follow it before the next table/array-table header)
	if lastIdx >= 0 {
		for i := lastIdx + 1; i < len(doc.root.Children); i++ {
			k := doc.root.Children[i].Kind
			if k == cst.NodeTable || k == cst.NodeArrayTable {
				break
			}
			lastIdx = i
		}
	}

	blankLine := &cst.Node{Kind: cst.NodeNewline, Raw: []byte("\n")}

	if lastIdx >= 0 {
		// Insert after the last entry's content
		insertIdx := lastIdx + 1
		newChildren := make([]*cst.Node, 0, len(doc.root.Children)+2)
		newChildren = append(newChildren, doc.root.Children[:insertIdx]...)
		newChildren = append(newChildren, blankLine, newNode)
		newChildren = append(newChildren, doc.root.Children[insertIdx:]...)
		doc.root.Children = newChildren
	} else {
		// No existing entries — append at end
		doc.root.Children = append(doc.root.Children, blankLine, newNode)
	}

	return newNode
}

// RemoveArrayTableEntry removes a [[key]] section and its body from the document.
func (doc *Document) RemoveArrayTableEntry(node *cst.Node) error {
	startIdx := -1
	for i, child := range doc.root.Children {
		if child == node {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return fmt.Errorf("node not found in document")
	}

	// Find the end: everything up to the next table/array-table header or EOF
	endIdx := startIdx + 1
	for endIdx < len(doc.root.Children) {
		k := doc.root.Children[endIdx].Kind
		if k == cst.NodeTable || k == cst.NodeArrayTable {
			break
		}
		endIdx++
	}

	// Remove a preceding blank line if present
	if startIdx > 0 && doc.root.Children[startIdx-1].Kind == cst.NodeNewline {
		startIdx--
	}

	doc.root.Children = append(doc.root.Children[:startIdx], doc.root.Children[endIdx:]...)
	return nil
}
```

**Step 4: Run tests to verify they pass**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/document/ -run "TestAppendArrayTableEntry|TestRemoveArrayTableEntry" -v`
Expected: PASS

**Step 5: Run all document tests to check for regressions**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/document/ -v`
Expected: all PASS

**Step 6: Commit**

```
feat(document): add AppendArrayTableEntry and RemoveArrayTableEntry
```

---

### Task 4: Key-path parsing with [N] and [] index syntax

**Files:**
- Modify: `pkg/document/document.go`
- Test: `pkg/document/document_test.go`

**Step 1: Write the failing tests**

Add to `pkg/document/document_test.go`:

```go
func TestGetArrayTableByIndex(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\nport = 8080\n\n[[servers]]\nname = \"lux\"\nport = 9090\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	name, err := Get[string](doc, "servers[0].name")
	if err != nil {
		t.Fatal(err)
	}
	if name != "grit" {
		t.Fatalf("expected %q, got %q", "grit", name)
	}

	name, err = Get[string](doc, "servers[1].name")
	if err != nil {
		t.Fatal(err)
	}
	if name != "lux" {
		t.Fatalf("expected %q, got %q", "lux", name)
	}

	port, err := Get[int](doc, "servers[1].port")
	if err != nil {
		t.Fatal(err)
	}
	if port != 9090 {
		t.Fatalf("expected 9090, got %d", port)
	}
}

func TestGetArrayTableIndexOutOfRange(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Get[string](doc, "servers[5].name")
	if err == nil {
		t.Fatal("expected error for out of range index")
	}
}

func TestSetArrayTableByIndex(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\n\n[[servers]]\nname = \"lux\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	if err := doc.Set("servers[1].name", "moxy"); err != nil {
		t.Fatal(err)
	}
	expected := "[[servers]]\nname = \"grit\"\n\n[[servers]]\nname = \"moxy\"\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestSetArrayTableAppend(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	if err := doc.Set("servers[].name", "lux"); err != nil {
		t.Fatal(err)
	}
	expected := "[[servers]]\nname = \"grit\"\n\n[[servers]]\nname = \"lux\"\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestSetArrayTableAppendErrorOnGet(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Get[string](doc, "servers[].name")
	if err == nil {
		t.Fatal("expected error for append syntax in Get")
	}
}

func TestDeleteArrayTableEntry(t *testing.T) {
	input := []byte("[[servers]]\nname = \"a\"\n\n[[servers]]\nname = \"b\"\n\n[[servers]]\nname = \"c\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	if err := doc.Delete("servers[1]"); err != nil {
		t.Fatal(err)
	}

	// After deletion, what was [2] is now [1] (slice reindexing)
	name, err := Get[string](doc, "servers[1].name")
	if err != nil {
		t.Fatal(err)
	}
	if name != "c" {
		t.Fatalf("expected %q after reindex, got %q", "c", name)
	}

	expected := "[[servers]]\nname = \"a\"\n\n[[servers]]\nname = \"c\"\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestDeleteArrayTableKey(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\nport = 8080\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	if err := doc.Delete("servers[0].port"); err != nil {
		t.Fatal(err)
	}
	expected := "[[servers]]\nname = \"grit\"\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestDeleteArrayTableAppendSyntaxErrors(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	err = doc.Delete("servers[]")
	if err == nil {
		t.Fatal("expected error for append syntax in Delete")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/document/ -run "TestGetArrayTable|TestSetArrayTable|TestDeleteArrayTable" -v`
Expected: FAIL

**Step 3: Write the key-path parser and integrate into Get/Set/Delete**

Add a key segment type and parser to `pkg/document/document.go`:

```go
type keySegment struct {
	key      string
	hasIndex bool
	index    int  // -1 means append ([])
}

// parseKeyPath parses keys like "servers[0].name" into segments.
func parseKeyPath(key string) ([]keySegment, bool) {
	// Check if key contains array index syntax
	bracketIdx := strings.IndexByte(key, '[')
	if bracketIdx < 0 {
		return nil, false
	}

	var segments []keySegment
	first := key[:bracketIdx]
	rest := key[bracketIdx:]

	// Parse the index: [N] or []
	closeIdx := strings.IndexByte(rest, ']')
	if closeIdx < 0 {
		return nil, false
	}

	indexStr := rest[1:closeIdx]
	seg := keySegment{key: first, hasIndex: true}

	if indexStr == "" {
		seg.index = -1 // append
	} else {
		idx, err := strconv.Atoi(indexStr)
		if err != nil {
			return nil, false
		}
		seg.index = idx
	}

	segments = append(segments, seg)

	// Parse remaining dotted keys after the ]
	remaining := rest[closeIdx+1:]
	if len(remaining) > 0 && remaining[0] == '.' {
		remaining = remaining[1:]
		if remaining != "" {
			segments = append(segments, keySegment{key: remaining})
		}
	}

	return segments, true
}

// resolveArrayTableContainer finds the array-table container node for
// the given segments. Returns the container and the leaf key (if any).
func (doc *Document) resolveArrayTableContainer(segments []keySegment) (*cst.Node, string, error) {
	seg := segments[0]
	nodes := doc.FindArrayTableNodes(seg.key)

	if seg.index == -1 {
		// Append: create a new entry
		newNode := doc.AppendArrayTableEntry(seg.key)
		leafKey := ""
		if len(segments) > 1 {
			leafKey = segments[1].key
		}
		return newNode, leafKey, nil
	}

	if len(nodes) == 0 {
		return nil, "", fmt.Errorf("no array-of-tables entries for key %q", seg.key)
	}
	if seg.index >= len(nodes) {
		return nil, "", fmt.Errorf("index %d out of range (%d entries)", seg.index, len(nodes))
	}

	leafKey := ""
	if len(segments) > 1 {
		leafKey = segments[1].key
	}
	return nodes[seg.index], leafKey, nil
}
```

Update `Get` (replace the existing function at line 28):

```go
func Get[T any](doc *Document, key string) (T, error) {
	var zero T

	if segments, ok := parseKeyPath(key); ok {
		if segments[0].index == -1 {
			return zero, fmt.Errorf("append syntax [] only valid in Set")
		}
		container, leafKey, err := doc.resolveArrayTableContainer(segments)
		if err != nil {
			return zero, err
		}
		if leafKey == "" {
			return zero, fmt.Errorf("cannot Get entire array-table entry")
		}
		return GetFromContainer[T](doc, container, leafKey)
	}

	valueNode, err := findValueNode(doc.root, key)
	if err != nil {
		return zero, err
	}

	result, err := convertNode[T](valueNode)
	if err != nil {
		return zero, fmt.Errorf("key %q: %w", key, err)
	}

	return result, nil
}
```

Update `Set` (replace the existing function at line 45):

```go
func (doc *Document) Set(key string, value any) error {
	if segments, ok := parseKeyPath(key); ok {
		container, leafKey, err := doc.resolveArrayTableContainer(segments)
		if err != nil {
			return err
		}
		if leafKey == "" {
			return fmt.Errorf("cannot Set entire array-table entry, specify a field")
		}
		return doc.SetInContainer(container, leafKey, value)
	}

	encoded, nodeKind, err := encodeValue(value)
	if err != nil {
		return err
	}

	parts := strings.Split(key, ".")

	if len(parts) == 1 {
		return setInContainer(doc.root, parts[0], encoded, nodeKind)
	}

	tableName := strings.Join(parts[:len(parts)-1], ".")
	leafKey := parts[len(parts)-1]

	tableNode := findTableNode(doc.root, tableName)
	if tableNode == nil {
		return fmt.Errorf("table %q not found", tableName)
	}

	return setInContainer(tableNode, leafKey, encoded, nodeKind)
}
```

Update `Delete` (replace the existing function at line 70):

```go
func (doc *Document) Delete(key string) error {
	if segments, ok := parseKeyPath(key); ok {
		if segments[0].index == -1 {
			return fmt.Errorf("append syntax [] only valid in Set")
		}
		nodes := doc.FindArrayTableNodes(segments[0].key)
		if len(nodes) == 0 {
			return fmt.Errorf("no array-of-tables entries for key %q", segments[0].key)
		}
		if segments[0].index >= len(nodes) {
			return fmt.Errorf("index %d out of range (%d entries)", segments[0].index, len(nodes))
		}
		if len(segments) > 1 && segments[1].key != "" {
			// Delete a key within the entry
			return deleteFromContainer(nodes[segments[0].index], segments[1].key)
		}
		// Delete the entire entry
		return doc.RemoveArrayTableEntry(nodes[segments[0].index])
	}

	parts := strings.Split(key, ".")

	if len(parts) == 1 {
		return deleteFromContainer(doc.root, parts[0])
	}

	tableName := strings.Join(parts[:len(parts)-1], ".")
	leafKey := parts[len(parts)-1]

	tableNode := findTableNode(doc.root, tableName)
	if tableNode == nil {
		return fmt.Errorf("table %q not found", tableName)
	}

	return deleteFromContainer(tableNode, leafKey)
}
```

**Step 4: Run tests to verify they pass**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/document/ -run "TestGetArrayTable|TestSetArrayTable|TestDeleteArrayTable" -v`
Expected: PASS

**Step 5: Run all document tests to check for regressions**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/document/ -v`
Expected: all PASS

**Step 6: Commit**

```
feat(document): support [N] and [] index syntax in Get/Set/Delete key paths
```

---

### Task 5: Marshal decode for []StructType

**Files:**
- Modify: `pkg/marshal/marshal.go`
- Test: `pkg/marshal/marshal_test.go`

**Step 1: Write the failing test**

Add test struct and test to `pkg/marshal/marshal_test.go`:

```go
type Server struct {
	Name    string `toml:"name"`
	Command string `toml:"command"`
}

type ServersConfig struct {
	Title   string   `toml:"title"`
	Servers []Server `toml:"servers"`
}

func TestUnmarshalArrayOfTables(t *testing.T) {
	input := []byte("title = \"config\"\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n")
	var cfg ServersConfig
	_, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Title != "config" {
		t.Fatalf("expected Title %q, got %q", "config", cfg.Title)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].Name != "grit" {
		t.Fatalf("expected Servers[0].Name %q, got %q", "grit", cfg.Servers[0].Name)
	}
	if cfg.Servers[0].Command != "grit mcp" {
		t.Fatalf("expected Servers[0].Command %q, got %q", "grit mcp", cfg.Servers[0].Command)
	}
	if cfg.Servers[1].Name != "lux" {
		t.Fatalf("expected Servers[1].Name %q, got %q", "lux", cfg.Servers[1].Name)
	}
	if cfg.Servers[1].Command != "lux serve" {
		t.Fatalf("expected Servers[1].Command %q, got %q", "lux serve", cfg.Servers[1].Command)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/marshal/ -run TestUnmarshalArrayOfTables -v`
Expected: FAIL — `unsupported slice element type struct`

**Step 3: Write minimal implementation**

Modify `decodeSliceField` in `pkg/marshal/marshal.go` (line 145) to add a `reflect.Struct` case before the default:

```go
func decodeSliceField(doc *document.Document, fv reflect.Value, key string) error {
	elemType := fv.Type().Elem()

	switch elemType.Kind() {
	case reflect.Int:
		v, err := document.Get[[]int](doc, key)
		if err != nil {
			return err
		}
		fv.Set(reflect.ValueOf(v))

	case reflect.String:
		v, err := document.Get[[]string](doc, key)
		if err != nil {
			return err
		}
		fv.Set(reflect.ValueOf(v))

	case reflect.Struct:
		return decodeStructSliceField(doc, fv, key)

	default:
		return fmt.Errorf("unsupported slice element type %s for key %q", elemType.Kind(), key)
	}

	return nil
}

func decodeStructSliceField(doc *document.Document, fv reflect.Value, key string) error {
	nodes := doc.FindArrayTableNodes(key)
	if len(nodes) == 0 {
		return nil
	}

	slice := reflect.MakeSlice(fv.Type(), len(nodes), len(nodes))
	elemType := fv.Type().Elem()

	for i, node := range nodes {
		elem := slice.Index(i)
		for j := range elemType.NumField() {
			field := elemType.Field(j)
			name, ok := fieldTomlKey(field)
			if !ok {
				continue
			}
			fieldVal := elem.Field(j)
			v, err := document.GetFromContainer[string](doc, node, name)
			if err != nil {
				continue
			}
			if err := setFieldFromString(fieldVal, v); err != nil {
				return fmt.Errorf("field %q in %q[%d]: %w", name, key, i, err)
			}
		}
	}

	fv.Set(slice)
	return nil
}

func setFieldFromString(fv reflect.Value, s string) error {
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(s)
	case reflect.Int, reflect.Int64:
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		fv.SetInt(v)
	case reflect.Float64:
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		fv.SetFloat(v)
	case reflect.Bool:
		v, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		fv.SetBool(v)
	default:
		return fmt.Errorf("unsupported field type %s", fv.Kind())
	}
	return nil
}
```

Add `"strconv"` to the import block in marshal.go.

**Step 4: Run test to verify it passes**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/marshal/ -run TestUnmarshalArrayOfTables -v`
Expected: PASS

**Step 5: Commit**

```
feat(marshal): unmarshal []StructType from [[array-of-tables]]
```

---

### Task 6: Marshal encode for []StructType

**Files:**
- Modify: `pkg/marshal/marshal.go`
- Test: `pkg/marshal/marshal_test.go`

**Step 1: Write the failing test**

Add to `pkg/marshal/marshal_test.go`:

```go
func TestRoundTripArrayOfTables(t *testing.T) {
	input := []byte("# my servers\ntitle = \"config\"\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n")
	var cfg ServersConfig
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Modify existing entry
	cfg.Servers[1].Command = "lux mcp"
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := "# my servers\ntitle = \"config\"\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux mcp\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestRoundTripArrayOfTablesAppend(t *testing.T) {
	input := []byte("# my servers\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")
	var cfg ServersConfig
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	cfg.Servers = append(cfg.Servers, Server{Name: "lux", Command: "lux serve"})
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := "# my servers\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestRoundTripArrayOfTablesRemove(t *testing.T) {
	input := []byte("[[servers]]\nname = \"a\"\ncommand = \"a\"\n\n[[servers]]\nname = \"b\"\ncommand = \"b\"\n\n[[servers]]\nname = \"c\"\ncommand = \"c\"\n")
	var cfg ServersConfig
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Remove middle entry
	cfg.Servers = []Server{cfg.Servers[0], cfg.Servers[2]}
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := "[[servers]]\nname = \"a\"\ncommand = \"a\"\n\n[[servers]]\nname = \"c\"\ncommand = \"c\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestRoundTripArrayOfTablesNoChanges(t *testing.T) {
	input := []byte("# preserved\n\n[[servers]]\nname = \"grit\"  # inline\ncommand = \"grit mcp\"\n")
	var cfg ServersConfig
	doc, err := UnmarshalDocument(input, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := MarshalDocument(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(input) {
		t.Fatalf("expected byte-for-byte identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/marshal/ -run "TestRoundTripArrayOfTables" -v`
Expected: FAIL — encode doesn't handle struct slices yet

**Step 3: Write minimal implementation**

Modify `encodeSliceField` in `pkg/marshal/marshal.go` (line 221) to add a `reflect.Struct` case:

```go
func encodeSliceField(doc *document.Document, fv reflect.Value, key string) error {
	elemType := fv.Type().Elem()

	switch elemType.Kind() {
	case reflect.Int:
		s := make([]int, fv.Len())
		for i := range fv.Len() {
			s[i] = int(fv.Index(i).Int())
		}
		return doc.Set(key, s)

	case reflect.String:
		s := make([]string, fv.Len())
		for i := range fv.Len() {
			s[i] = fv.Index(i).String()
		}
		return doc.Set(key, s)

	case reflect.Struct:
		return encodeStructSliceField(doc, fv, key)

	default:
		return fmt.Errorf("unsupported slice element type %s for key %q", elemType.Kind(), key)
	}
}

func encodeStructSliceField(doc *document.Document, fv reflect.Value, key string) error {
	nodes := doc.FindArrayTableNodes(key)
	elemType := fv.Type().Elem()

	for i := range fv.Len() {
		var container *cst.Node
		if i < len(nodes) {
			container = nodes[i]
		} else {
			container = doc.AppendArrayTableEntry(key)
		}
		elem := fv.Index(i)
		for j := range elemType.NumField() {
			field := elemType.Field(j)
			name, ok := fieldTomlKey(field)
			if !ok {
				continue
			}
			if err := encodeField(doc, elem.Field(j), qualifiedKey(key+fmt.Sprintf("[%d]", i), name)); err != nil {
				return err
			}
		}
		_ = container // used for append tracking
	}

	// Remove trailing entries if slice shrank
	for i := fv.Len(); i < len(nodes); i++ {
		if err := doc.RemoveArrayTableEntry(nodes[i]); err != nil {
			return err
		}
	}

	return nil
}
```

Wait — that uses the key-path API for encoding which re-traverses. The design says marshal should use container helpers directly. Revise:

```go
func encodeStructSliceField(doc *document.Document, fv reflect.Value, key string) error {
	nodes := doc.FindArrayTableNodes(key)
	elemType := fv.Type().Elem()

	for i := range fv.Len() {
		var container *cst.Node
		if i < len(nodes) {
			container = nodes[i]
		} else {
			container = doc.AppendArrayTableEntry(key)
		}
		elem := fv.Index(i)
		for j := range elemType.NumField() {
			field := elemType.Field(j)
			name, ok := fieldTomlKey(field)
			if !ok {
				continue
			}
			val := encodeFieldValue(elem.Field(j))
			if val == nil {
				continue
			}
			if err := doc.SetInContainer(container, name, val); err != nil {
				return err
			}
		}
	}

	// Remove trailing entries if slice shrank
	for i := fv.Len(); i < len(nodes); i++ {
		if err := doc.RemoveArrayTableEntry(nodes[i]); err != nil {
			return err
		}
	}

	return nil
}

func encodeFieldValue(fv reflect.Value) any {
	switch fv.Kind() {
	case reflect.String:
		return fv.String()
	case reflect.Int:
		return int(fv.Int())
	case reflect.Int64:
		return fv.Int()
	case reflect.Float64:
		return fv.Float()
	case reflect.Bool:
		return fv.Bool()
	default:
		return nil
	}
}
```

Add `"github.com/amarbel-llc/tommy/pkg/cst"` to the import block in marshal.go.

**Step 4: Run tests to verify they pass**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./pkg/marshal/ -run "TestRoundTripArrayOfTables" -v`
Expected: PASS

**Step 5: Run all tests to check for regressions**

Run: `cd /home/sasha/eng/repos/tommy/.worktrees/firm-plum && go test ./... -v`
Expected: all PASS

**Step 6: Commit**

```
feat(marshal): encode []StructType as [[array-of-tables]] with round-trip support

Closes #5
```
