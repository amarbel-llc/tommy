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
puts the restamp in that same commit. This is the supported repair lane for a
**go.mod** repo (madder and the like).

```
conformist-tommy-codegen [--check] [--flake-attr <attr>]
```

Without *--flake-attr* it runs *tommy generate* per directive file (with
*--check*, *tommy generate --check*), which needs a go.mod — the supported path
above. With *--flake-attr* it never runs the generator in the checkout: it
builds _.#<attr>_ (a *goNixCodegen* *repair*) and applies the patch with
*git apply*. With *--check* it fails and prints the patch instead. *go*, *nix*
and *git* come from PATH when present, else from pinned packages, so the lane
also works in the bare environment of a git hook.

*tommy.conformistModule.<system>* wires *conformist-tommy-codegen* as
_[linter.tommy-codegen]_: a whole-tree repair (*passes-files* = false) triggered
by a staged _\*.go_, _flake.lock_ or _go.nix_, whose outputs the _--staged_ hook
restages (*restage-repair-outputs*, *stage-new-outputs*, *stage-deleted-outputs*).
Import it as is for the go.mod path above; a go.nix module additionally sets
*tommy.codegen.flakeAttr* below.

*DEPRECATED (tommy#144):* the *--flake-attr* / *tommy.codegen.flakeAttr* route
below, for a **go.nix** module (no go.mod), in favor of conformist's generic
codegen-repair linter (conformist#124, in progress). That linter finds any
*checks.<system>.<name>* carrying *passthru.codegenPatch* — igloo#80's
*codegenCheck*/*codegenPatch* contract, which every *goNixCodegen.check* already
carries — and reaches every go.nix repo through conformist's *eng* preset with
no per-repo flake wiring. No godyn repo has adopted *--flake-attr* yet, so there
is nothing to migrate off it; a new go.nix consumer should wait for the generic
linter instead of wiring the option below.

A go.nix module opts in to the deprecated route by exposing the *repair* as a
package (above) and naming it in its conformist module evaluation:

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

*DecodeTStrict(input []byte) (\*TDocument, error)*
	*DecodeT* for input that must be exactly what it claims. See STRICT
	DECODE.

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

# STRICT DECODE

*DecodeT* is lenient: it builds a document from any bytes and skips or coerces
a value it cannot place, so an unterminated string swallows the lines after it
and _hooks = 42_ decodes into a string field as _"42"_. That behaviour is kept,
so input written against it stays decodable. *DecodeTStrict* is the opt-in
alternative. It decodes the same way and returns the same handle, but fails
where *DecodeT* would guess:

- Input that is not well-formed TOML returns a *\*cst.SyntaxError* carrying
  *Line*, *Column* and *Msg*, printed as _line 1, column 18: unterminated
  string_. It covers unterminated strings, arrays, inline tables and table
  headers, a key-value missing its key, _=_ or value, two statements on one
  line, missing separators in arrays and inline tables, and malformed keys,
  integers, floats and date-times.
- A value whose TOML type does not match its field returns *\*cst.TypeError*
  values carrying *Key* (the dotted path *Undecoded* uses), *Line*, *Column*
  and *Msg*, printed as _line 2, column 1: key "hooks": expected string, got
  integer_. Every mismatch in the input is reported, joined with
  *errors.Join*; *errors.As* finds the first.

A key no field claims is not an error in either decoder; read *Undecoded* to
reject those.

Strict decode does not check escape sequences or control characters inside
strings, and does not catch an integer that overflows a narrower Go type
(_int8_, _uint16_, ...). It rejects two things TOML allows or *Encode* writes:
a date-time with a space between date and time, and an integer in a float
field (_ratio = 1_), which is how *Encode* writes a whole-number float. A
*TOMLUnmarshaler* field receives whatever value is present and decides for
itself. A cross-package struct reports mismatches only once its own package
is regenerated with a *tommy* that has *DecodeTStrict*.

The same checks are available without a generated decoder:
*document.ParseStrict* and *cst.ParseStrict* parse and validate,
*cst.Validate* validates a tree already parsed, and *(\*cst.Value).TypeErrors*
reports mismatches on a model a decoder has walked.

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
