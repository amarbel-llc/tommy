package cst

import "testing"

// Issue #137: a generated decoder dropped a [parent.subtable] when the parent
// header carried a scalar key before the sub-table header. The report's two
// shapes differ ONLY by that preceding scalar; if Decompose were the layer at
// fault they would yield different models. This pins the model: the failing
// header shape and the inline-table workaround both collapse to the same
// canonical value — so the model layer is correct and the drop is downstream.
func TestDecomposeIssue137ScalarBeforeSubtable(t *testing.T) {
	const want = `{direnv={dotenv={PIGGY_STORE_DIR=L("/x")},envrc=L(["source_up"])}}`
	spellings := []string{
		// FAILS in the report: scalar key on the parent before the sub-table.
		"[direnv]\nenvrc = [\"source_up\"]\n\n[direnv.dotenv]\nPIGGY_STORE_DIR = \"/x\"\n",
		// Inline-table spelling of the sub-table (the report's confirmed workaround).
		"[direnv]\nenvrc = [\"source_up\"]\ndotenv = { PIGGY_STORE_DIR = \"/x\" }\n",
	}
	for _, s := range spellings {
		if got := dumpValue(mustDecompose(t, s)); got != want {
			t.Fatalf("spelling collapse mismatch\n  spelling: %q\n  got:  %s\n  want: %s", s, got, want)
		}
	}
}
