package generate

import "testing"

// Opt-in strict decode: DecodeXStrict rejects malformed TOML and values whose
// TOML type does not match the Go field, while DecodeX keeps accepting both.
func TestIntegrationStrictDecode(t *testing.T) {
	dir, root := nestingSetup(t)
	nestingGoMod(t, dir, root, "strict")
	writeFixture(t, dir, "config.go", `package strict

import "errors"

//go:generate tommy generate
type Config struct {
	FileExtension string            `+"`toml:\"file-extension,omitempty\"`"+`
	Hooks         string            `+"`toml:\"hooks\"`"+`
	Port          int               `+"`toml:\"port\"`"+`
	Enabled       bool              `+"`toml:\"enabled\"`"+`
	Tags          []string          `+"`toml:\"tags\"`"+`
	Ratio         float64           `+"`toml:\"ratio\"`"+`
	Level        Level             `+"`toml:\"level\"`"+`
	Server        Server            `+"`toml:\"server\"`"+`
	Extra         *Extra            `+"`toml:\"extra\"`"+`
	Env           map[string]string `+"`toml:\"env\"`"+`
	Routes        map[string]Route  `+"`toml:\"routes\"`"+`
	Items         []Item            `+"`toml:\"items\"`"+`
}

type Server struct {
	Host string `+"`toml:\"host\"`"+`
}

type Extra struct {
	Note string `+"`toml:\"note\"`"+`
}

type Route struct {
	Path string `+"`toml:\"path\"`"+`
}

type Item struct {
	Name string `+"`toml:\"name\"`"+`
}

type Level struct{ name string }

func (l Level) MarshalText() ([]byte, error) { return []byte(l.name), nil }

func (l *Level) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		return errors.New("empty level")
	}
	l.name = string(b)
	return nil
}
`)
	writeFixture(t, dir, "config_test.go", `package strict

import (
	"errors"
	"strings"
	"testing"

	"code.linenisgreat.com/tommy/pkg/cst"
)

const unterminated = "file-extension = \"md\nhooks = [unterminated\n"

func TestLenientDecodeUnchanged(t *testing.T) {
	doc, err := DecodeConfig([]byte(unterminated))
	if err != nil {
		t.Fatalf("DecodeConfig must keep accepting malformed input, got %v", err)
	}
	if got := doc.Data().FileExtension; got != "\"md\nhooks = [unterminated\n" {
		t.Fatalf("FileExtension=%q", got)
	}
	doc, err = DecodeConfig([]byte("hooks = 42\n"))
	if err != nil {
		t.Fatalf("DecodeConfig must keep coercing, got %v", err)
	}
	if got := doc.Data().Hooks; got != "42" {
		t.Fatalf("Hooks=%q", got)
	}
}

func TestStrictRejectsMalformed(t *testing.T) {
	_, err := DecodeConfigStrict([]byte(unterminated))
	var se *cst.SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("want *cst.SyntaxError, got %v", err)
	}
	if se.Line != 1 || se.Column != 18 || !strings.Contains(se.Msg, "unterminated string") {
		t.Fatalf("got %v", err)
	}
	for _, input := range []string{
		"hooks = [unterminated\n",
		"hooks = \"a\" port = 1\n",
		"hooks\n",
		"[server\nhost = \"h\"\n",
		"port = 12abc\n",
	} {
		if _, err := DecodeConfigStrict([]byte(input)); !errors.As(err, &se) {
			t.Errorf("%q: want *cst.SyntaxError, got %v", input, err)
		}
	}
}

func TestStrictRejectsMistyped(t *testing.T) {
	_, err := DecodeConfigStrict([]byte("port = 1\nhooks = 42\n"))
	var te *cst.TypeError
	if !errors.As(err, &te) {
		t.Fatalf("want *cst.TypeError, got %v", err)
	}
	if te.Key != "hooks" || te.Line != 2 || te.Column != 1 {
		t.Fatalf("got key %q at %d:%d", te.Key, te.Line, te.Column)
	}
	if want := "line 2, column 1: key \"hooks\": expected string, got integer"; err.Error() != want {
		t.Fatalf("error=%q want %q", err.Error(), want)
	}

	for key, input := range map[string]string{
		"enabled":      "enabled = \"yes\"\n",
		"port":         "port = \"80\"\n",
		"hooks":        "hooks = { a = 1 }\n",
		"tags":         "tags = \"x\"\n",
		"level":        "level = 3\n",
		"server":       "server = 5\n",
		"server.host":  "[server]\nhost = true\n",
		"extra":        "extra = \"x\"\n",
		"extra.note":   "[extra]\nnote = 1.5\n",
		"env":          "env = 1\n",
		"env.A":        "[env]\nA = 1\nB = \"ok\"\n",
		"routes.r":     "[routes]\nr = 1\n",
		"routes.r.path": "[routes.r]\npath = 2\n",
		"items":        "items = 3\n",
		"items[0].name": "[[items]]\nname = 2\n",
	} {
		_, err := DecodeConfigStrict([]byte(input))
		if !errors.As(err, &te) {
			t.Errorf("%q: want *cst.TypeError, got %v", input, err)
			continue
		}
		if te.Key != key {
			t.Errorf("%q: error names key %q, want %q (%v)", input, te.Key, key, err)
		}
	}

	if _, err := DecodeConfigStrict([]byte("tags = [\"a\", 1]\n")); !errors.As(err, &te) || te.Key != "tags" {
		t.Errorf("heterogeneous array: got %v", err)
	}
	_, err = DecodeConfigStrict([]byte("hooks = 1\nenabled = 2\n"))
	if err == nil || !strings.Contains(err.Error(), "\"hooks\"") || !strings.Contains(err.Error(), "\"enabled\"") {
		t.Errorf("every mistyped key should be reported, got %v", err)
	}
}

func TestStrictLeavesUnknownKeysToUndecoded(t *testing.T) {
	doc, err := DecodeConfigStrict([]byte("hooks = \"h\"\nno-such-key = \"x\"\n"))
	if err != nil {
		t.Fatalf("an unknown key is not a strict error: %v", err)
	}
	if u := doc.Undecoded(); len(u) != 1 || u[0] != "no-such-key" {
		t.Fatalf("Undecoded=%v", u)
	}
}

const wellFormed = "# config\nfile-extension = \"md\" # ext\nhooks = \"\"\"\nline1\nline2\"\"\"\nport = 8_080\nenabled = true\ntags = [\"a\", \"b\"]\nlevel = \"high\"\nenv = { FOO = \"bar\" }\n\n[server]\nhost = 'h'\n\n[extra]\nnote = \"n\"\n\n[routes.r]\npath = \"/\"\n\n[[items]]\nname = \"one\"\n\n[[items]]\nname = \"two\"\n"

func TestStrictAcceptsWellFormedAndRoundTrips(t *testing.T) {
	doc, err := DecodeConfigStrict([]byte(wellFormed))
	if err != nil {
		t.Fatal(err)
	}
	d := doc.Data()
	if d.FileExtension != "md" || d.Hooks != "line1\nline2" || d.Port != 8080 || !d.Enabled {
		t.Fatalf("data=%+v", d)
	}
	if len(d.Tags) != 2 || d.Level.name != "high" || d.Env["FOO"] != "bar" || d.Server.Host != "h" {
		t.Fatalf("data=%+v", d)
	}
	if d.Extra == nil || d.Extra.Note != "n" || d.Routes["r"].Path != "/" || len(d.Items) != 2 || d.Items[1].Name != "two" {
		t.Fatalf("data=%+v", d)
	}
	if u := doc.Undecoded(); len(u) != 0 {
		t.Fatalf("Undecoded=%v", u)
	}

	// Strict decode hands back the same document as the lenient one: whatever
	// Encode does to this input, it does identically for both.
	lenient, err := DecodeConfig([]byte(wellFormed))
	if err != nil {
		t.Fatal(err)
	}
	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	lenientOut, err := lenient.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(lenientOut) {
		t.Fatalf("strict and lenient encode differently:\nstrict:\n%s\nlenient:\n%s", out, lenientOut)
	}

	// Comments and layout survive a strict decode + encode untouched.
	const commented = "# config\n\nfile-extension = \"md\"   # ext\nhooks = \"h\"\n\n# the port\nport   = 8080\nenabled = true\ntags = [\"a\", \"b\"]\nlevel = \"high\"\n\n[server]\nhost = \"h\" # where\n\n[[items]]\nname = \"one\"\n"
	doc, err = DecodeConfigStrict([]byte(commented))
	if err != nil {
		t.Fatal(err)
	}
	doc.Data().Port = 9090
	out, err = doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(commented, "8080", "9090", 1); string(out) != want {
		t.Fatalf("strict decode must not disturb formatting:\n%s", out)
	}

	// Empty spellings are not type errors.
	if _, err := DecodeConfigStrict([]byte("tags = []\nitems = []\nenv = {}\n")); err != nil {
		t.Fatalf("empty containers: %v", err)
	}
}

// TOML keeps integers and floats apart, so strict decode rejects an integer in
// a float field. The lenient decoder silently leaves the field at zero.
func TestStrictRejectsIntegerInFloatField(t *testing.T) {
	var te *cst.TypeError
	if _, err := DecodeConfigStrict([]byte("ratio = 1\n")); !errors.As(err, &te) || te.Key != "ratio" {
		t.Fatalf("want a type error for ratio, got %v", err)
	}
	doc, err := DecodeConfigStrict([]byte("ratio = 1.5\n"))
	if err != nil || doc.Data().Ratio != 1.5 {
		t.Fatalf("ratio = 1.5: %v", err)
	}
	doc.Data().Ratio = 2
	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("a whole-number float encodes as %q", out)
}

func TestStrictStillRunsDecodeErrors(t *testing.T) {
	if _, err := DecodeConfigStrict([]byte("hooks = \"a\"\nhooks = \"b\"\n")); err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("want duplicate key error, got %v", err)
	}
	if _, err := DecodeConfigStrict([]byte("level = \"\"\n")); err == nil || !strings.Contains(err.Error(), "empty level") {
		t.Fatalf("want UnmarshalText error, got %v", err)
	}
}
`)
	nestingRun(t, dir)
}

// A cross-package struct decodes through its own package's DecodeXInto, so a
// mistyped value inside it must surface from the outer strict decoder too.
func TestIntegrationStrictDecodeDelegated(t *testing.T) {
	dir, root := nestingSetup(t)
	nestingGoMod(t, dir, root, "strictdel")
	writeFixture(t, dir+"/dep", "dep.go", `package dep

//go:generate tommy generate
type Inner struct {
	Name string `+"`toml:\"name\"`"+`
	Size int    `+"`toml:\"size\"`"+`
}
`)
	if err := Generate(dir+"/dep", "dep.go"); err != nil {
		t.Fatalf("Generate dep: %v", err)
	}
	writeFixture(t, dir, "config.go", `package strictdel

import "example.com/strictdel/dep"

//go:generate tommy generate
type Config struct {
	Title string      `+"`toml:\"title\"`"+`
	Inner dep.Inner   `+"`toml:\"inner\"`"+`
	More  []dep.Inner `+"`toml:\"more\"`"+`
}
`)
	writeFixture(t, dir, "config_test.go", `package strictdel

import (
	"errors"
	"testing"

	"code.linenisgreat.com/tommy/pkg/cst"
)

func TestStrictDelegated(t *testing.T) {
	if _, err := DecodeConfigStrict([]byte("title = \"t\"\n\n[inner]\nname = \"n\"\nsize = 1\n\n[[more]]\nname = \"m\"\n")); err != nil {
		t.Fatal(err)
	}
	var te *cst.TypeError
	for key, input := range map[string]string{
		"inner.name":   "[inner]\nname = 7\n",
		"inner.size":   "[inner]\nsize = \"big\"\n",
		"more[0].name": "[[more]]\nname = false\n",
	} {
		_, err := DecodeConfigStrict([]byte(input))
		if !errors.As(err, &te) || te.Key != key {
			t.Errorf("%q: want a type error for %q, got %v", input, key, err)
		}
		if _, err := DecodeConfig([]byte(input)); err != nil {
			t.Errorf("%q: lenient decode must keep accepting, got %v", input, err)
		}
	}
}
`)
	nestingRun(t, dir)
}
