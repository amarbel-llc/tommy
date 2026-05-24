package generate

import (
	"strings"
	"testing"

	"golang.org/x/tools/imports"
)

// Regression test for #69: imports.Process with FormatOnly must
// group stdlib and third-party imports into separate blocks.
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

	out, err := imports.Process("/tmp/x.go", src, &imports.Options{
		Comments:   true,
		TabIndent:  true,
		TabWidth:   8,
		FormatOnly: true,
	})
	if err != nil {
		t.Fatalf("imports.Process: %v", err)
	}

	got := string(out)
	// Stdlib block: "fmt"\n\t"strings"\n\t"time" appears in order, before third-party.
	wantStdlibBlock := "\t\"fmt\"\n\t\"strings\"\n\t\"time\"\n\n\t\"github.com/aaa/zzz\"\n\t\"github.com/foo/bar\"\n"
	if !strings.Contains(got, wantStdlibBlock) {
		t.Fatalf("imports not grouped/sorted as expected:\n--- got ---\n%s", got)
	}
}
