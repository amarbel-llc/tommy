package cst

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateRejectsMalformed(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		line, column int
		msg          string
	}{
		{"unterminated basic string swallows following lines", "file-extension = \"md\nhooks = [unterminated\n", 1, 18, "unterminated string"},
		{"unterminated basic string at EOF", "a = \"md", 1, 5, "unterminated string"},
		{"unterminated literal string", "a = 'md\nb = 1\n", 1, 5, "unterminated string"},
		{"unterminated multiline basic string", "a = \"\"\"\nmd\nb = 1\n", 1, 5, "unterminated string"},
		{"unterminated multiline literal string", "a = '''\nmd\n", 1, 5, "unterminated string"},
		{"unterminated array", "ok = 1\nhooks = [1, 2\n", 2, 9, "unterminated array"},
		{"bare word value", "ok = 1\nhooks = unterminated\n", 2, 9, "invalid value"},
		{"bare word array element", "hooks = [unterminated]\n", 1, 10, "invalid value"},
		{"unterminated inline table", "t = {a = 1\n", 1, 5, "unterminated inline table"},
		{"missing equals", "ok = 1\nhooks\n", 2, 1, "expected '='"},
		{"missing value", "hooks =\n", 1, 8, "missing value"},
		{"missing value at EOF", "hooks =", 1, 8, "missing value"},
		{"two key-values on one line", "a = 1 b = 2\n", 1, 7, "expected newline"},
		{"unterminated table header", "[table\nkey = 1\n", 1, 1, "unterminated table header"},
		{"unterminated array-table header", "[[items]\nkey = 1\n", 1, 1, "unterminated table header"},
		{"empty table header", "[]\n", 1, 1, "empty table header"},
		{"key-value on the header line", "[table] key = 1\n", 1, 9, "expected newline"},
		{"malformed integer", "a = 12abc\n", 1, 5, "invalid integer"},
		{"leading zero integer", "a = 007\n", 1, 5, "invalid integer"},
		{"malformed float", "a = 1.2.3\n", 1, 5, "invalid float"},
		{"two bare keys", "a b = 1\n", 1, 1, "expected '='"},
		{"missing array comma", "a = [1 2]\n", 1, 8, "expected ','"},
		{"error inside a table body", "[t]\nok = 1\nbad = \"x\n", 3, 7, "unterminated string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, err := Parse([]byte(tt.input))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			err = Validate(root)
			if err == nil {
				t.Fatalf("Validate accepted %q", tt.input)
			}
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("want *SyntaxError, got %T: %v", err, err)
			}
			if se.Line != tt.line || se.Column != tt.column {
				t.Errorf("position = %d:%d, want %d:%d (%v)", se.Line, se.Column, tt.line, tt.column, err)
			}
			if !strings.Contains(se.Msg, tt.msg) {
				t.Errorf("message %q does not contain %q", se.Msg, tt.msg)
			}
			if _, perr := ParseStrict([]byte(tt.input)); perr == nil {
				t.Errorf("ParseStrict accepted %q", tt.input)
			}
		})
	}
}

// Validate must accept the well-formed TOML the lenient parser round-trips
// today, and must not disturb the tree it inspects.
func TestValidateAcceptsWellFormed(t *testing.T) {
	inputs := []string{
		"",
		"  \n",
		"\n\n\n",
		"# just a comment\n",
		"key = \"value\"\n",
		"key = \"value\"",
		"key\t=\t\"value\" # comment\n",
		"int = -17\nhex = 0xDEAD_BEEF\noct = 0o755\nbin = 0b11010110\nz = 0\np = +42\nu = 1_000_000\n",
		"flt = 3.14\nn = -0.01\ne = 5e+22\nm = 1_000.5e+2\ni = inf\nj = -inf\nk = nan\nl = +nan\n",
		"t = true\nf = false\n",
		"s = \"tab\\there \\\"quoted\\\" C:\\\\path \\u00E9\"\n",
		"s = \"\"\"\nhello\nworld\"\"\"\n",
		"s = \"\"\"with \"quotes\" inside\"\"\"\"\n",
		"s = 'no\\escapes'\nm = '''\nno\\escapes\nhere'''\n",
		"a = \"\"\nb = ''\nc = \"\"\"\"\"\"\nd = ''''''\n",
		"dt = 1979-05-27T07:32:00Z\nl = 1979-05-27T07:32:00.999999\nd = 1979-05-27\nt = 07:32:00\no = 1979-05-27T07:32:00-05:00\n",
		"[table]\nkey = \"value\"\n",
		"[a.b.c]\nkey = \"value\"\n\n[\"quoted key\".d]\nk = 1\n",
		"[a] # trailing comment\nk1 = 1\n\n[b]\nk2 = 2",
		"[[products]]\nname = \"Hammer\"\n\n[[products]]\nname = \"Nail\"\n",
		"arr = [1, 2, 3,]\nmixed = [\"a\", 1, true]\nnested = [[1, 2], [3, 4]]\nempty = []\n",
		"arr = [\n  # comment\n  1,\n  2,\n]\n",
		"point = {x = 1, y = 2}\nn = {b = {c = [1, 2, {d = true}]}}\ne = {}\n",
		"data = [{x = 1}, {x = 2}]\n",
		"a.b = \"value\"\n\"a\".c = 1\n'lit'.d = 2\n",
		"[fruit]\napple.color = \"red\"\n",
		"key = \"value\"\r\n[table]\r\nkey = 1\r\n",
		"# Config file\n\ntitle = \"Example\"\n\n[owner]\nname = \"Tom\"\n\n[database]\nports = [8001, 8001, 8002]\nenabled = true\n",
	}
	for _, input := range inputs {
		root, err := ParseStrict([]byte(input))
		if err != nil {
			t.Errorf("ParseStrict rejected %q: %v", input, err)
			continue
		}
		if got := string(root.Bytes()); got != input {
			t.Errorf("round-trip changed:\n  input: %q\n  got:   %q", input, got)
		}
	}
}

func TestSyntaxErrorMessageCarriesPosition(t *testing.T) {
	_, err := ParseStrict([]byte("ok = 1\nhooks = [1, 2\n"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if want := "line 2, column 9: unterminated array"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// decodeString mimics a generated string-field reader: look the key up as a
// known field, extract leniently, and record that a string was expected.
func decodeString(model *Value, key string) {
	if v, ok := model.Field(key); ok && v.Kind == VLeaf {
		if _, xok := ExtractString(v.Leaf); xok {
			v.MarkConsumedString()
		}
	}
}

func decodeBool(model *Value, key string) {
	if v, ok := model.Field(key); ok && v.Kind == VLeaf {
		if _, xok := ExtractBool(v.Leaf); xok {
			v.MarkConsumed()
		}
	}
}

func typeErrorsOf(t *testing.T, err error) []*TypeError {
	t.Helper()
	if err == nil {
		return nil
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("want a joined error, got %T: %v", err, err)
	}
	var out []*TypeError
	for _, e := range joined.Unwrap() {
		var te *TypeError
		if !errors.As(e, &te) {
			t.Fatalf("want *TypeError, got %T: %v", e, e)
		}
		out = append(out, te)
	}
	return out
}

func TestTypeErrorsIntegerIntoStringField(t *testing.T) {
	model, err := DecomposeBytes([]byte("name = \"ok\"\nhooks = 42\n"))
	if err != nil {
		t.Fatal(err)
	}
	decodeString(model, "name")
	decodeString(model, "hooks")

	errs := typeErrorsOf(t, model.TypeErrors())
	if len(errs) != 1 {
		t.Fatalf("want 1 type error, got %v", errs)
	}
	te := errs[0]
	if te.Key != "hooks" || te.Line != 2 || te.Column != 1 {
		t.Errorf("got key %q at %d:%d, want hooks at 2:1", te.Key, te.Line, te.Column)
	}
	if want := `line 2, column 1: key "hooks": expected string, got integer`; te.Error() != want {
		t.Errorf("error = %q, want %q", te.Error(), want)
	}
}

func TestTypeErrorsKnownFieldLeftUnread(t *testing.T) {
	model, err := DecomposeBytes([]byte("enabled = \"yes\"\n\n[server]\nname = 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	decodeBool(model, "enabled")
	// A reader that expects `server` to be a scalar leaf: a table does not match.
	decodeString(model, "server")

	errs := typeErrorsOf(t, model.TypeErrors())
	if len(errs) != 2 {
		t.Fatalf("want 2 type errors, got %v", errs)
	}
	if errs[0].Key != "enabled" || !strings.Contains(errs[0].Msg, "string") {
		t.Errorf("first error = %v", errs[0])
	}
	if errs[1].Key != "server" || !strings.Contains(errs[1].Msg, "table") {
		t.Errorf("second error = %v", errs[1])
	}
}

func TestTypeErrorsIgnoreUnknownKeys(t *testing.T) {
	model, err := DecomposeBytes([]byte("name = \"ok\"\nno-such-key = \"x\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	decodeString(model, "name")
	if err := model.TypeErrors(); err != nil {
		t.Fatalf("an unknown key is not a type error: %v", err)
	}
	if u := model.Undecoded(); len(u) != 1 || u[0] != "no-such-key" {
		t.Fatalf("Undecoded = %v, want [no-such-key]", u)
	}
}

func TestTypeErrorsMapEntries(t *testing.T) {
	model, err := DecomposeBytes([]byte("[env]\nFOO = \"bar\"\nBAD = 7\nSUB = {a = 1}\n"))
	if err != nil {
		t.Fatal(err)
	}
	env, _ := model.Field("env")
	env.MarkSeen()
	env.MarkEntriesKnown()
	for i := range env.Fields {
		f := &env.Fields[i]
		if f.Val.Kind == VLeaf {
			if _, ok := ExtractString(f.Val.Leaf); ok {
				f.Val.MarkConsumedString()
			}
		}
	}
	errs := typeErrorsOf(t, model.TypeErrors())
	if len(errs) != 2 || errs[0].Key != "env.BAD" || errs[1].Key != "env.SUB" {
		t.Fatalf("want errors for env.BAD and env.SUB, got %v", errs)
	}
}

func TestTypeErrorsTextAcceptsDateTime(t *testing.T) {
	model, err := DecomposeBytes([]byte("at = 1979-05-27T07:32:00Z\ns = \"x\"\nn = 5\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"at", "s", "n"} {
		v, _ := model.Field(k)
		v.MarkConsumedText()
	}
	errs := typeErrorsOf(t, model.TypeErrors())
	if len(errs) != 1 || errs[0].Key != "n" {
		t.Fatalf("want one error for n, got %v", errs)
	}
}
