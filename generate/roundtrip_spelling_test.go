package generate

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Spelling-rewrite fuzzer (#107). The two round-trip fuzzers
// (TestRoundTripFuzz / TestRoundTripFuzzDelegation) only ever feed the decoder
// TOML that tommy's OWN encoder produced — one canonical spelling per kind. So
// the decoder's acceptance of the OTHER valid TOML spellings of the same value
// (inline tables vs sub-tables, dotted keys vs nested tables, inline arrays vs
// array-of-tables) is untested; that blind spot is exactly how #106 slipped
// through. This fuzzer reuses the shape generator but, per case, re-spells the
// canonical encoder output via the pkg/cst Respell* API and checks each spelling
// against the decoder.
//
// Classification is DYNAMIC, from observed bytes — no shape prediction (a
// shape-based expected-pass predicate was tried and empirically rejected:
// whether a respell fires, and whether the fired form decodes, depends on
// encoder details — nesting depth, leaf-ness, empty entries, quoted keys — that
// a shallow shape walk cannot capture). Per variant:
//
//   - respell NO-OP (bytes unchanged): the variant TOML is literally the
//     canonical encoding, so it MUST round-trip — a failure is a hard error
//     (the real regression guard: a canonical form must always decode).
//   - respell CHANGED: the alternative spelling may or may not be accepted by
//     the decoder (most are not yet — #107 is staged fuzzer-first). The outcome
//     is logged (xfail/xpass) but never fails CI. When a later cycle teaches the
//     decoder a spelling, its own integration tests guard it; this fuzzer simply
//     starts logging xpass for that shape.
//
// Supersedes the hand-rolled duality audit (a throwaway `debug-duality-audit`
// recipe) that probed these same dualities against a single fixed fixture.

// spellingVariants are the rewrites applied to each canonical encoding, paired
// with the cst.Respell* function the generated body calls. "canonical" is the
// identity (no rewrite) — the same guarantee the existing fuzzers prove.
var spellingVariants = []struct {
	name    string
	respell string // cst.Respell* function name, or "" for identity
}{
	{"canonical", ""},
	{"inline-table-leaf", "RespellInlineTables"},
	{"dotted-key", "RespellDottedKeys"},
	{"inline-array-of-tables", "RespellInlineArrays"},
}

// respellHelperSrc is injected into the generated fuzz_test.go. checkSpelling
// classifies one (case, variant) dynamically: a no-op respell must round-trip
// (hard fail otherwise); a changed respell is logged as xfail/xpass and never
// fails CI. Relies on the fixture importing pkg/cst and "bytes".
const respellHelperSrc = `// spellingChanged counts how many (case, variant) pairs the respell actually
// rewrote — non-zero proves the fuzzer exercised alternative spellings rather
// than silently no-opping into a canonical-only run (a coverage guard).
var spellingChanged int

func checkSpelling(t *testing.T, name, variant string, want any, canonical, respelled []byte, got any, decErr error, undecoded []string) {
	t.Helper()
	changed := !bytes.Equal(respelled, canonical)
	pass := decErr == nil && len(undecoded) == 0 && reflect.DeepEqual(got, want)
	if !changed {
		// The rewrite was inapplicable, so this IS the canonical encoding: it must
		// round-trip. A failure here is a real regression, not a spelling gap.
		if !pass {
			t.Fatalf("%s/%s: canonical (unchanged) form failed to round-trip\nerr=%v undecoded=%v\nwant: %s\ngot:  %s\ntoml:\n%s", name, variant, decErr, undecoded, dump(want), dump(got), respelled)
		}
		return
	}
	spellingChanged++
	if pass {
		t.Logf("%s/%s: xpass — alternative spelling now round-trips", name, variant)
	} else {
		t.Logf("%s/%s: xfail — alternative spelling not yet accepted", name, variant)
	}
}

`

// roundTripSpellingCaseBody renders the per-case t.Run block: decode empty, set
// want, encode canonical, then run every spelling variant through checkSpelling.
func roundTripSpellingCaseBody(name, value string) string {
	var variants strings.Builder
	for i, v := range spellingVariants {
		if i > 0 {
			variants.WriteString(", ")
		}
		respellRef := "nil"
		if v.respell != "" {
			respellRef = "cst." + v.respell
		}
		fmt.Fprintf(&variants, "{%q, %s}", v.name, respellRef)
	}
	return fmt.Sprintf(`	t.Run(%q, func(t *testing.T) {
		want := %s
		d, err := Decode%s([]byte(""))
		if err != nil { t.Fatalf("decode empty: %%v", err) }
		*d.Data() = want
		out, err := d.Encode()
		if err != nil { t.Fatalf("encode: %%v", err) }
		for _, v := range []struct {
			name    string
			respell func([]byte) ([]byte, error)
		}{%s} {
			respelled := out
			if v.respell != nil {
				rs, rerr := v.respell(out)
				if rerr != nil { t.Fatalf("%%s: respell error: %%v", v.name, rerr) }
				respelled = rs
			}
			d2, derr := Decode%s(respelled)
			var got %s
			var undec []string
			if derr == nil {
				got = *d2.Data()
				undec = d2.Undecoded()
			}
			checkSpelling(t, %q, v.name, want, out, respelled, got, derr, undec)
		}
	})
`, name, value, name, variants.String(), name, name, name)
}

// spellingFuzzTestSource wraps the spelling case bodies into a complete
// fuzz_test.go, importing pkg/cst (for the Respell* calls) and "bytes"
// alongside the shared helpers, and injecting checkSpelling.
func spellingFuzzTestSource(testBodies string) string {
	imp := "import (\n\t\"bytes\"\n\t\"fmt\"\n\t\"reflect\"\n\t\"sort\"\n\t\"strings\"\n\t\"testing\"\n\n\t\"github.com/amarbel-llc/tommy/pkg/cst\"\n)\n\n"
	coverage := "\tt.Logf(\"spelling fuzz: %d (case,variant) pairs rewrote the canonical encoding\", spellingChanged)\n\tif spellingChanged == 0 {\n\t\tt.Fatal(\"no variant rewrote the canonical encoding — the spelling fuzzer exercised nothing beyond canonical\")\n\t}\n"
	return "package fuzz\n\n" + imp +
		"func ptr[T any](v T) *T { return &v }\n\n" +
		dumpHelperSrc +
		respellHelperSrc +
		"func TestRoundTripSpelling(t *testing.T) {\n" + testBodies + coverage + "}\n"
}

func TestRoundTripSpellingFuzz(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	seed := int64(fuzzEnvInt("TOMMY_FUZZ_SEED", 1))
	cases := fuzzEnvInt("TOMMY_FUZZ_CASES", 96)
	const depth = 4
	t.Logf("spelling round-trip fuzz: seed=%d cases=%d depth=%d", seed, cases, depth)

	g := &shapeGen{rng: rand.New(rand.NewSource(seed)), typeDefs: &strings.Builder{}}
	var testBodies strings.Builder
	for i := 0; i < cases; i++ {
		name := fmt.Sprintf("Case%d", i)
		fields, body := g.genFields(depth - 1)
		fmt.Fprintf(g.typeDefs, "//go:generate tommy generate\ntype %s struct {\n%s}\n\n", name, body)
		g.maybeEmitValidate(g.typeDefs, name)
		value := g.genValue(&td{kind: "struct", stName: name, fields: fields})
		testBodies.WriteString(roundTripSpellingCaseBody(name, value))
	}

	configSrc := "package fuzz\n\n" + g.typeDefs.String()
	testSrc := spellingFuzzTestSource(testBodies.String())

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/fuzz", "", "go 1.26", "",
		"require github.com/amarbel-llc/tommy v0.0.0", "",
		"replace github.com/amarbel-llc/tommy => " + repoRoot, "",
	}, "\n"))
	writeFixture(t, dir, "config.go", configSrc)
	writeFixture(t, dir, "fuzz_test.go", testSrc)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate (seed=%d): %v\n--- config.go ---\n%s", seed, err, configSrc)
	}

	cmd := exec.Command("go", "test", "-count=1", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("spelling round-trip fuzz failed (seed=%d):\n%s\n--- config.go ---\n%s\n--- fuzz_test.go ---\n%s", seed, out, configSrc, testSrc)
	}
}
