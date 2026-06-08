package generate

// BuildVersion and BuildCommit identify the tommy build that produced a
// generated file. They are stamped into the generated header (RenderFile) so a
// binary↔library version skew — a stale `tommy` codegen binary run against a
// newer tommy library — is visible in the output rather than silent until
// compile (tommy#125).
//
// cmd/tommy/main sets these from the ldflags-injected main.version / main.commit
// (eng-versioning(7): single-binary repos embed -X main.version / main.commit).
// They default to dev/unknown for in-process use (the ./generate tests, `go run`,
// or a `go build` without ldflags). A dirty build's commit carries a "-dirty"
// suffix (flake passes self.dirtyShortRev), so even uncommitted codegen is
// distinguishable.
var (
	BuildVersion = "dev"
	BuildCommit  = "unknown"
)
