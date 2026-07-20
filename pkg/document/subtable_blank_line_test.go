package document

import (
	"testing"

	"code.linenisgreat.com/tommy/pkg/cst"
)

// TestSubtableReSetPreservesBlankLineSeparator regression-tests issue #75:
// after `cst.DeleteAllValues` + `cst.SetAny` on a populated subtable, the
// blank line that separated the subtable from its next sibling must remain
// between the (re-set) KV and the next sibling, not jump to between the
// subtable header and the KV.
//
// The bug surfaced via tommy-generated `Encode()` consumers (e.g.
// amarbel-llc/spinclass internal/sweatfile/sweatfile_test.go::
// TestRoundTripPreservesComments) once the kvInsertIndex end-of-container
// case stopped accounting for trailing blank lines.
func TestSubtableReSetPreservesBlankLineSeparator(t *testing.T) {
	input := []byte("# Global config\n\n[git]\nexcludes = [\".claude/\", \".direnv/\"]\n\n[claude]\nallow = [\"Bash(git *)\"]\n\n[direnv]\nenvrc = [\"source_up\", \"use flake\"]\n\n[direnv.dotenv]\nFOO = \"bar\"\n\n[hooks]\n# install deps on create\ncreate = \"npm install\"\nstop = \"just test\"\ndisallow-main-worktree = true\n")

	doc, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// Naked parse → Bytes() does round-trip cleanly (the regression doesn't
	// surface here). The bug only triggers when generated code re-sets
	// values on a populated subtable — `cst.DeleteAllValues` followed by
	// `cst.SetAny` for each key, which is what tommy-generated `Encode()`
	// does for subtable map fields like `direnv.dotenv` or
	// `session-entry.env`.
	if string(doc.Bytes()) != string(input) {
		t.Errorf("plain round-trip already drifted:\n--- want ---\n%s--- got ---\n%s", input, doc.Bytes())
	}

	// Simulate what the generated Encode() does for direnv.dotenv:
	direnvNode := doc.FindTable("direnv")
	if direnvNode == nil {
		t.Fatal("[direnv] table missing")
	}
	dotenvNode := doc.FindTableInContainer(direnvNode, "dotenv")
	if dotenvNode == nil {
		t.Fatal("[direnv.dotenv] subtable missing")
	}
	cst.DeleteAllValues(dotenvNode)
	if err := cst.SetAny(dotenvNode, "FOO", "bar"); err != nil {
		t.Fatalf("SetAny: %v", err)
	}

	output := doc.Bytes()
	if string(output) != string(input) {
		t.Errorf("subtable re-set round-trip mismatch:\n--- want ---\n%s--- got ---\n%s", input, output)
	}
}
