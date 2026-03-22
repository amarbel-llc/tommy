package document

import (
	"testing"
)

func TestGetString(t *testing.T) {
	input := []byte("name = \"tommy\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Get[string](doc, "name")
	if err != nil {
		t.Fatal(err)
	}
	if got != "tommy" {
		t.Fatalf("expected %q, got %q", "tommy", got)
	}
}

func TestGetInt(t *testing.T) {
	input := []byte("port = 8080\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Get[int](doc, "port")
	if err != nil {
		t.Fatal(err)
	}
	if got != 8080 {
		t.Fatalf("expected 8080, got %d", got)
	}
}

func TestGetInt64(t *testing.T) {
	input := []byte("big = 9999999999\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Get[int64](doc, "big")
	if err != nil {
		t.Fatal(err)
	}
	if got != 9999999999 {
		t.Fatalf("expected 9999999999, got %d", got)
	}
}

func TestGetBool(t *testing.T) {
	input := []byte("enabled = true\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Get[bool](doc, "enabled")
	if err != nil {
		t.Fatal(err)
	}
	if got != true {
		t.Fatalf("expected true, got %v", got)
	}
}

func TestGetFloat(t *testing.T) {
	input := []byte("pi = 3.14\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Get[float64](doc, "pi")
	if err != nil {
		t.Fatal(err)
	}
	if got != 3.14 {
		t.Fatalf("expected 3.14, got %f", got)
	}
}

func TestGetIntSlice(t *testing.T) {
	input := []byte("buckets = [2, 4, 8]\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Get[[]int](doc, "buckets")
	if err != nil {
		t.Fatal(err)
	}
	expected := []int{2, 4, 8}
	if len(got) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("expected %v, got %v", expected, got)
		}
	}
}

func TestGetStringSlice(t *testing.T) {
	input := []byte("tags = [\"a\", \"b\", \"c\"]\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Get[[]string](doc, "tags")
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"a", "b", "c"}
	if len(got) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("expected %v, got %v", expected, got)
		}
	}
}

func TestGetNestedKey(t *testing.T) {
	input := []byte("[storage]\npath = \"/data\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Get[string](doc, "storage.path")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/data" {
		t.Fatalf("expected %q, got %q", "/data", got)
	}
}

func TestSetPreservesComments(t *testing.T) {
	input := []byte("# config\nkey = \"old\" # important\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set("key", "new"); err != nil {
		t.Fatal(err)
	}
	expected := "# config\nkey = \"new\" # important\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestSetNestedKey(t *testing.T) {
	input := []byte("[storage]\npath = \"/old\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set("storage.path", "/new"); err != nil {
		t.Fatal(err)
	}
	expected := "[storage]\npath = \"/new\"\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestSetNewKey(t *testing.T) {
	input := []byte("a = 1\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set("b", 2); err != nil {
		t.Fatal(err)
	}
	got := string(doc.Bytes())
	if got != "a = 1\nb = 2\n" {
		t.Fatalf("expected 'a = 1\\nb = 2\\n', got %q", got)
	}
}

func TestSetNewKeyInTable(t *testing.T) {
	input := []byte("[server]\nhost = \"localhost\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set("server.port", 8080); err != nil {
		t.Fatal(err)
	}
	expected := "[server]\nhost = \"localhost\"\nport = 8080\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestDeletePreservesOtherEntries(t *testing.T) {
	input := []byte("a = 1\nb = 2\nc = 3\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Delete("b"); err != nil {
		t.Fatal(err)
	}
	expected := "a = 1\nc = 3\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestDeleteNestedKey(t *testing.T) {
	input := []byte("[db]\nhost = \"localhost\"\nport = 5432\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Delete("db.host"); err != nil {
		t.Fatal(err)
	}
	expected := "[db]\nport = 5432\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestSetBool(t *testing.T) {
	input := []byte("flag = false\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set("flag", true); err != nil {
		t.Fatal(err)
	}
	expected := "flag = true\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSetFloat(t *testing.T) {
	input := []byte("rate = 1.5\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set("rate", 2.5); err != nil {
		t.Fatal(err)
	}
	expected := "rate = 2.5\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSetIntSlice(t *testing.T) {
	input := []byte("nums = [1]\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set("nums", []int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	expected := "nums = [1, 2, 3]\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSetStringSlice(t *testing.T) {
	input := []byte("tags = [\"x\"]\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set("tags", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	expected := "tags = [\"a\", \"b\"]\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestGetKeyNotFound(t *testing.T) {
	input := []byte("a = 1\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Get[int](doc, "missing")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

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

func TestDeleteKeyNotFound(t *testing.T) {
	input := []byte("a = 1\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	err = doc.Delete("missing")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

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

func TestRemoveArrayTableEntryFirst(t *testing.T) {
	input := []byte("[[servers]]\nname = \"a\"\n\n[[servers]]\nname = \"b\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	nodes := doc.FindArrayTableNodes("servers")
	if err := doc.RemoveArrayTableEntry(nodes[0]); err != nil {
		t.Fatal(err)
	}

	expected := "[[servers]]\nname = \"b\"\n"
	got := string(doc.Bytes())
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

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

func TestGetRawFromContainerString(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	nodes := doc.FindArrayTableNodes("servers")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	raw, err := GetRawFromContainer(doc, nodes[0], "command")
	if err != nil {
		t.Fatal(err)
	}
	s, ok := raw.(string)
	if !ok {
		t.Fatalf("expected string, got %T", raw)
	}
	if s != "grit mcp" {
		t.Fatalf("expected %q, got %q", "grit mcp", s)
	}
}

func TestGetRawFromContainerBool(t *testing.T) {
	input := []byte("[[entries]]\nenabled = true\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	nodes := doc.FindArrayTableNodes("entries")
	raw, err := GetRawFromContainer(doc, nodes[0], "enabled")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := raw.(bool)
	if !ok {
		t.Fatalf("expected bool, got %T", raw)
	}
	if !b {
		t.Fatal("expected true")
	}
}

func TestGetRawFromContainerArray(t *testing.T) {
	input := []byte("[[entries]]\ntags = [\"a\", \"b\"]\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	nodes := doc.FindArrayTableNodes("entries")
	raw, err := GetRawFromContainer(doc, nodes[0], "tags")
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", raw)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(arr))
	}
	if arr[0] != "a" || arr[1] != "b" {
		t.Fatalf("expected [a, b], got %v", arr)
	}
}

func TestGetRawFromContainerNotFound(t *testing.T) {
	input := []byte("[[entries]]\nname = \"x\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	nodes := doc.FindArrayTableNodes("entries")
	_, err = GetRawFromContainer(doc, nodes[0], "missing")
	if err == nil {
		t.Fatal("expected error for missing key")
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

func TestFindTableInContainer(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\n[servers.annotations]\nreadOnlyHint = true\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	nodes := doc.FindArrayTableNodes("servers")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	tableNode := doc.FindTableInContainer(nodes[0], "annotations")
	if tableNode == nil {
		t.Fatal("expected to find annotations table")
	}
	v, err := GetFromContainer[bool](doc, tableNode, "readOnlyHint")
	if err != nil {
		t.Fatal(err)
	}
	if !v {
		t.Fatal("expected readOnlyHint true")
	}
}

func TestFindTableInContainerNotFound(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	nodes := doc.FindArrayTableNodes("servers")
	tableNode := doc.FindTableInContainer(nodes[0], "missing")
	if tableNode != nil {
		t.Fatal("expected nil for missing table")
	}
}
