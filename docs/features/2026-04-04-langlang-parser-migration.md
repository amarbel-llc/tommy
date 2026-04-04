---
date: 2026-04-04
promotion-criteria: prototype langlang TOML parser decodes the benchmark fixture
  with \<500 allocs while preserving byte-for-byte round-trip fidelity
status: exploring
---

# Migrate Parser/CST to langlang for Zero-Copy Decoding

## Problem Statement

Tommy's decode path allocates \~1,715 objects per decode of a moderate config
file (13 fields, 5 array-table entries with nested structs and maps). Profiling
shows \~80% of these allocations come from the CST layer: `*Node` pointer
allocations, `Children []*Node` slice allocations, and lexer `Raw []byte` copies
from the ring buffer arena. The competing pelletier/go-toml library achieves 205
allocations for the same input by using a flat node pool with integer indices
and zero-copy byte references into the original input.

Tommy's CST exists for format-preserving round-trip editing --- a feature
neither pelletier nor BurntSushi offers. The question is whether the CST can be
redesigned to be allocation-efficient without sacrificing mutation capability.
amarbel-llc/langlang provides a PEG parser generator with exactly the right CST
representation: flat `[]node` pool, `NodeID uint32` indices, zero-copy
`start/end` offsets, and a views codegen mode that produces zero-allocation
typed accessors.

## Interface

Replace tommy's hand-written lexer (`internal/lexer/`) and CST parser
(`pkg/cst/parser.go`) with a langlang-generated parser from a TOML v1.0 PEG
grammar. The generated code produces a `tree` with:

- Flat `[]node` pool (no per-node heap allocation)
- Packed `[]NodeID` children array with range indices (no `[]*Node` slices)
- Zero-copy `start/end` byte offsets into the original `[]byte` input
- `views` codegen for typed, zero-allocation read accessors

The decode codegen path (`TOMMY_CODEGEN_IR=jen`) would use langlang views
instead of CST node walking. The encode path would use a streaming writer that
interleaves original input bytes (via offset ranges from the immutable tree)
with updated values, replacing the current in-place CST mutation approach.

Target: decode allocations drop from \~1,715 to \<500 for the benchmark fixture,
closing the gap with pelletier while retaining format preservation.

## Limitations

- **Langlang tree is immutable after parse.** The current encode path mutates
  CST nodes via `SetValue`, `EnsureChildTable`, etc. This must be replaced with
  a write-streaming approach that reconstructs output from the original tree +
  changes, rather than patching the tree in-place.

- **TOML v1.0 PEG grammar does not exist.** The langlang examples include a
  \~40-line TOML subset. A full grammar covering array-of-tables, literal
  strings, multiline strings, hex/octal/binary integers, special floats,
  datetime, and comments needs to be written and validated against the TOML
  conformance suite.

- **Langlang requires in-memory `[]byte` input.** Tommy's current lexer streams
  from `io.Reader` via a ring buffer. The langlang parser requires the full
  input in memory. For typical config files (\<1MB) this is fine; for streaming
  scenarios it would require buffering.

- **Pre-1.0 API.** Langlang's API is not stable. Tommy would depend on a
  specific commit or fork until langlang reaches 1.0.

- **Encode-as-writer is a new architecture.** The streaming encode approach is
  well-established in compiler tooling but represents a significant departure
  from tommy's current `Document` mutation model. The `Document` API would need
  to be preserved for users who use it directly (not via codegen).

## More Information

- Benchmark data: tommy-jen 86µs/1715 allocs, pelletier 33µs/205 allocs,
  BurntSushi 145µs/1212 allocs (commit 5afae60)
- Langlang tree design: `repos/langlang/go/tree.go` (SoA flat pool)
- Langlang views codegen: `repos/langlang/go/gen.go` (zero-alloc accessors)
- Partial TOML grammar: `repos/langlang/go/examples/toml-extract/toml.peg`
- tommy CST node: `pkg/cst/node.go` (pointer-based tree)
- tommy lexer: `internal/lexer/lexer.go` (ring buffer + arena)
- Related issue: amarbel-llc/langlang#11 (full TOML v1.0 PEG grammar)
