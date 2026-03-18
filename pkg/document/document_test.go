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
