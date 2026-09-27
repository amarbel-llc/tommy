tommy-generate(1)

# NAME

tommy-generate - generate type-safe TOML Decode/Encode methods

# SYNOPSIS

*//go:generate tommy generate*

*tommy generate --check*

# DESCRIPTION

*tommy generate* is a code generator invoked via a *//go:generate* directive.
For each struct in the current file that sits under such a directive and is
annotated with *toml* struct tags, it emits a companion file containing
round-trip-preserving *Decode*/*Encode* methods.

The generator must be run via *go generate* because it reads the *GOFILE*
environment variable to determine which file to analyze. Running *tommy
generate* directly from a shell exits with an error.

# INVOCATION

Place the directive on a line above (or adjacent to) the struct(s) to be
generated:

```
//go:generate tommy generate
type Config struct {
	Server  string `toml:"server"`
	Port    int    `toml:"port,omitempty"`
	Workers int    `toml:"workers"`
}
```

Then:

```
go generate ./...
```

# DIAGNOSTICS

On each run, *tommy generate* prints the resolved output path and the producing
*tommy* build to standard error:

```
tommy 0.4.1 (c113f1f): writing internal/config/config_tommy.go
```

so a wrong target (an output-filename mismatch) or a stale codegen binary is
visible at a glance rather than discovered later.

# CHECK MODE

*tommy generate --check* regenerates in memory and compares the result against
the existing generated file *without writing it*, exiting non-zero if they
differ or it is missing. Use it in CI to catch a generated file that is out of date —
or one produced by a different *tommy* version, which the header stamp (see
*OUTPUT*) makes a content difference. It reads *GOFILE* like generation, so run
it in the same *go generate* environment; alternatively a CI step may
*go generate ./...* and assert a clean *git diff*.

# GO.NIX MODULES

*tommy generate* type-loads the package with *go/packages*, which runs *go list*
and so needs a Go module: an enclosing *go.mod* and a *go* toolchain. A module
described by *go.nix* (igloo FDR 0008, see *godyn*(7)) has no go.mod in its
checkout, so *tommy generate* there exits with an error naming the alternatives.
Run codegen inside nix instead, where the go.mod is rendered from go.nix and the
dependencies are vendored, offline:

```
checks.<system>.codegen = myapp.passthru.codegenCheck {
  command = "go generate ./...";
  nativeBuildInputs = [ tommy.packages.<system>.default ];
};
```

tommy's flake packages that check together with a matching repair as
*tommy.lib.<system>.goNixCodegen*:

```
tommyCodegen = tommy.lib.${system}.goNixCodegen {
  module = myapp;   # a buildGodynModule / buildGoAuto result
  # command ? "go generate -run tommy ./..."
  # nativeBuildInputs ? [ ]   (tommy is always included)
  # exclude ? [ ]
};
checks.tommy-codegen = tommyCodegen.check;
packages.tommy-codegen-repair = tommyCodegen.repair;
```

*check* is the module's *passthru.codegenCheck* with those arguments. *repair* is
the same derivation — the same vendored tree, the same *command* — except that it
writes the difference from the source tree to _$out/patch_ (*git apply -p2*;
empty when the files are current) instead of failing on it. Applying the patch
therefore makes *check* pass. *repair* is pure: a rebuild with unchanged sources
and *tommy* pin is a cache hit, but a change to either (most commits that trigger
the lane) reruns the command in nix, a build the size of the check's. The patch
is relative to the
module's source root, which must be the flake root.

To write regenerated files back by hand, use igloo's escape hatch (*godyn-go*)
with *tommy* in *goRun*'s *nativeBuildInputs*, or run the repair lane below.

# REPAIR LANE

The *conformist-tommy-codegen* driver (the *conformist-tommy-codegen* package of
tommy's flake) is a *conformist* repair command. Run from the tree root, it
regenerates every file carrying a *//go:generate tommy generate* directive, so
a *conformist* pre-commit hook keeps the generated files, header stamp
included, current in every commit. That matters on a *tommy* bump: a lock-only
commit restamps every generated header (see *OUTPUT*), and the hook's repair
puts the restamp in that same commit.

```
conformist-tommy-codegen [--check] [--flake-attr <attr>]
```

Without *--flake-attr* it runs *tommy generate* per directive file (with
*--check*, *tommy generate --check*), which needs a go.mod. With *--flake-attr*
it never runs the generator in the checkout: it builds _.#<attr>_ (a
*goNixCodegen* *repair*) and applies the patch with *git apply*. With *--check*
it fails and prints the patch instead. *go*, *nix* and *git* come from PATH when
present, else from pinned packages, so the lane also works in the bare
environment of a git hook.

*tommy.conformistModule.<system>* wires it as _[linter.tommy-codegen]_: a
whole-tree repair (*passes-files* = false) triggered by a staged _\*.go_,
_flake.lock_ or _go.nix_, whose outputs the _--staged_ hook restages
(*restage-repair-outputs*, *stage-new-outputs*, *stage-deleted-outputs*).

A go.nix module opts in by exposing the *repair* as a package (above) and
naming it in its conformist module evaluation:

```
conformist.lib.evalModule pkgs {
  imports = [ tommy.conformistModule.${system} ./conformist.nix ];
  tommy.codegen.flakeAttr = "tommy-codegen-repair";
  ...
};
```

A repo that writes its conformist settings by hand sets the same linter the
module does (the module is the reference; prefer importing it):

```
settings.linter.tommy-codegen = {
  command = "true";
  "repair-command" = lib.getExe tommy.packages.${system}.conformist-tommy-codegen;
  "repair-options" = [ "--flake-attr" "tommy-codegen-repair" ];
  includes = [ "*.go" "flake.lock" "go.nix" ];
  "passes-files" = false;
  "restage-repair-outputs" = true;
  "stage-new-outputs" = true;
  "stage-deleted-outputs" = true;
};
```

and runs that evaluation's *build.preCommit* as its git pre-commit hook. The
build reads the checkout through the flake, which sees only tracked files: a new
source file must be staged (or *git add -N*'d) before the repair can see it.

# ENVIRONMENT

*GOFILE*
	Set by *go generate*. Names the source file containing the directive.
	Required. Absence is an error.

The generator also respects *GOPACKAGE* indirectly through *go/packages*
resolution of the working directory.

# OUTPUT

For input file _name.go_, the generator writes _name_tommy.go_ in the same
directory, overwriting any previous contents. The header records the *tommy*
build that produced the file so a binary/library version skew (a stale codegen
binary run against a newer *tommy* library) is visible rather than silent until
compile:

```
// Code generated by tommy <version> (<commit>); DO NOT EDIT.
```

_<version>_ and _<commit>_ are the *tommy* binary's embedded *version* and
*commit* (see *eng-versioning*(7)); a dirty build's commit carries a *-dirty*
suffix, and a non-release build reads *tommy dev (unknown)*. If a regenerated
file's version disagrees with the *tommy* library your code compiles against,
the codegen binary is stale — update it (re-enter the devshell / rebuild) before
regenerating.

Generated files import the *tommy* CST and document packages, plus any
packages reachable from tagged fields.

# GENERATED API

For each tagged struct *T*, the following package-level symbols are emitted:

*DecodeT(input []byte) (\*TDocument, error)*
	Parse TOML input, populate a *T*, and return a handle that retains the
	CST for round-trip editing.

*(\*TDocument) Data() \*T*
	Return a pointer to the decoded value.

*(\*TDocument) Encode() ([]byte, error)*
	Write the current *T* back into the retained CST and serialize. Fields
	unchanged since decode keep their original formatting and comments.

*(\*TDocument) Undecoded() []string*
	Return dotted key paths present in the input but not consumed by any
	field. Use this to warn on unknown configuration keys.

*(\*TDocument) Comment(key), SetComment(key, s)*
	Read or write the comment block attached above a key.

*(\*TDocument) InlineComment(key), SetInlineComment(key, s)*
	Read or write the trailing comment on the same line as a key.

*DecodeTInto(data \*T, doc \*document.Document, container \*cst.Node, consumed map[string]bool, keyPrefix string) error*
	Decode *T* from an existing document context. Called by parents that
	embed *T* as a cross-package struct field (the *delegated struct* kind;
	see *tommy-struct-tags*(7)).

*EncodeTFrom(data \*T, doc \*document.Document, container \*cst.Node) error*
	Encode *T* into an existing document context. The cross-package
	counterpart to *EncodeTInto*.

# VALIDATION

If *T* implements *Validate() error*, both *DecodeT* and *(\*TDocument).Encode*
call it automatically. *DecodeT* calls it after all fields are set; *Encode*
calls it before writing to the CST. A non-nil error from *Validate* aborts
the operation and is returned to the caller.

# STRUCT TAG SYNTAX

Only fields with a *toml* struct tag are considered. The tag name must match
the TOML key. Supported modifiers are *omitempty* and *multiline*. See
*tommy-struct-tags*(7) for the full reference of tag syntax, supported field
kinds, and supported Go interfaces.

# EXIT STATUS

*0*
	Generation succeeded.

*1*
	*GOFILE* unset, working directory unreadable, no enclosing *go.mod* (see
	*GO.NIX MODULES*), package analysis failed,
	or code emission failed. Diagnostic is printed to stderr.

# SEE ALSO

*tommy*(1), *tommy-fmt*(1), *tommy-struct-tags*(7), *go-generate*(1)
