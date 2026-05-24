package generate

import (
	"strings"
	"testing"

	"golang.org/x/tools/imports"
)

// Regression test for #69: goimportsOpts must group stdlib and
// third-party imports into separate blocks.
func TestImportsProcessGroups(t *testing.T) {
	src := []byte(`package x

import (
	"fmt"
	"github.com/foo/bar"
	"strings"
	"github.com/aaa/zzz"
	"time"
)

var _ = fmt.Sprintf
var _ = bar.X
var _ = strings.Contains
var _ = zzz.Y
var _ = time.Now
`)

	out, err := imports.Process("imports_grouping_test.go", src, goimportsOpts)
	if err != nil {
		t.Fatalf("imports.Process: %v", err)
	}

	importBlock := extractImportBlock(t, string(out))
	wantStdlib := []string{`"fmt"`, `"strings"`, `"time"`}
	wantThirdParty := []string{`"github.com/aaa/zzz"`, `"github.com/foo/bar"`}

	groups := splitImportGroups(importBlock)
	if len(groups) != 2 {
		t.Fatalf("want 2 import groups (stdlib + third-party), got %d:\n%s", len(groups), importBlock)
	}
	if !equalLines(groups[0], wantStdlib) {
		t.Errorf("stdlib group:\nwant %v\ngot  %v", wantStdlib, groups[0])
	}
	if !equalLines(groups[1], wantThirdParty) {
		t.Errorf("third-party group:\nwant %v\ngot  %v", wantThirdParty, groups[1])
	}
}

// extractImportBlock returns the content between `import (` and `)`.
func extractImportBlock(t *testing.T, src string) string {
	t.Helper()
	start := strings.Index(src, "import (\n")
	if start == -1 {
		t.Fatalf("no import block in output:\n%s", src)
	}
	rest := src[start+len("import (\n"):]
	end := strings.Index(rest, "\n)")
	if end == -1 {
		t.Fatalf("unterminated import block:\n%s", src)
	}
	return rest[:end]
}

// splitImportGroups splits on blank lines, returning each group's
// trimmed import-path lines.
func splitImportGroups(block string) [][]string {
	var groups [][]string
	for _, raw := range strings.Split(block, "\n\n") {
		var lines []string
		for _, line := range strings.Split(raw, "\n") {
			if s := strings.TrimSpace(line); s != "" {
				lines = append(lines, s)
			}
		}
		if len(lines) > 0 {
			groups = append(groups, lines)
		}
	}
	return groups
}

func equalLines(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
