# Why cutting-garden's generated tommy headers did not restamp (2026-10-26)

Labels: **R** read (source, issue, git history), **V** observed by running,
**I** inferred. As of this draft no V has been taken: the host is under a memory
hold (no nix builds, evals or heavy lanes). Section "Reproduction (V)" is a plan.

## Question

After tommy moved to b4977c3, the nikulin cascade halted at cutting-garden's
`checks.tommy-codegen`: four `*_tommy.go` headers were stale and nothing
restamped them at commit. tommy#143 ("repair lane: support godyn repos") is
closed. Operator hypothesis: a repair-ENVIRONMENT problem (like the spinclass
pre-merge build trees).

## Findings

### 1. What #143 closed, and what tommy ships for go.nix repos

- R: #143 was closed by tommy 8da2981 (`Closes #143`, 2026-09-27): a go.nix
  route for tommy's own driver. `lib.<system>.goNixCodegen { module; ... }`
  returns `check` and `repair`; `conformist-tommy-codegen --flake-attr <attr>`
  builds `.#<attr>` and `git apply`s `$out/patch`; the conformist module option
  `tommy.codegen.flakeAttr` selects it. cd24044 simplified it; 3324973
  (`Closes #144`) rebased `repair` onto igloo's `passthru.codegenPatch`
  (igloo#80) and marked the `--flake-attr` route DEPRECATED in favour of
  conformist's generic `codegen-repair` linter. #143 has zero comments.
- R: this route is OPT-IN twice over: the consumer must call `goNixCodegen`,
  expose the result as a flake package, and set `tommy.codegen.flakeAttr`.
  #144 says "no godyn repo has adopted goNixCodegen yet".
- R: so "#143 fixed" is true for tommy's side only. It never touched
  cutting-garden, which does not use it.

### 2. Which mechanism cutting-garden actually uses

- R: cutting-garden's flake (`flake.nix`, comment above `conformistTommyModule`)
  states "the former tommy-codegen ... repair lanes are gone" (go.nix removed the
  checkout go.mod). It does NOT import tommy's `conformistModule`, so
  `conformist-tommy-codegen` is not in its hooks at all. It inlines only the
  `tommy fmt` formatter.
- R: drift gate = `checks.tommy-codegen`, `cuttingGarden.passthru.codegenCheck`
  with `command = "go generate -run tommy ./..."` and
  `nativeBuildInputs = [ tommy.packages.default dagnabitPinned ]`, i.e. the tommy
  of cutting-garden's LOCKED `tommy` input. (This is igloo's check, not tommy's
  and not conformist's.)
- R: repair = conformist's generic `linters.codegen-repair` (conformist
  `nix/linters/codegen-repair.nix`, `conformist codegen-repair`), enabled in
  cutting-garden's `conformistCodegenEval` by cutting-garden ef39faf
  (2026-09-30) because `presets.eng` is deliberately omitted from that eval.
  It discovers `checks.<system>.*.passthru.codegenPatch`, builds each patch with
  nix, applies it, repeats to convergence.
- R: wiring into hooks: `sweatfile` sets `pre-commit = "conformist-pre-commit"`
  (= `conformistCodegenEval.config.build.preCommit`, `conformist --staged
  --exit-zero-on-fix`); `conformist-repair` (same eval, `--commit --amend`) is on
  the devShell PATH for spinclass's merge-repair phase. The sweatfile has NO
  `repair =` entry, only `pre-merge = "just"`.
- R: the linter is gated by trigger globs `*.go`, `flake.lock`, `go.nix`
  (`passes-files = false`), so a lock-only commit fires it.
- R: soft failure by design: discovery or per-check build failure, or no git
  worktree, WARNS and exits 0 (conformist FDR 0001, `--strict` promotes).
  Warnings are logged at Warn since conformist#127. Only a patch that was built
  but will not apply fails the hook.

### 3. The cascade

- R: `circus/eng/bin/update-repo-in-session.bash` runs inside an `sc run`
  worktree session with the devshell via direnv; it does `nix flake update`, then
  `git add flake.nix flake.lock && git commit`, "so the worktree's
  [hooks].pre-commit fires". The commit-time hook is the ONLY restamp point;
  sc run then runs `[hooks].pre-merge = just` (the gate, which is what failed).
  The script itself never regenerates and never calls `codegen-repair`.
- R (evidence the lane worked before): cutting-garden 3904eba (2026-10-22,
  trailer `circus-cascade: nikulin-20261022T221430Z`, tommy 1b6b24b -> bcebdc5)
  changes `flake.lock` AND the four `*_tommy.go` files in one commit; the
  working tree headers read `0.5.0 (bcebdc5)`, matching the lock. So on 10-22
  the same cascade, same repo, restamped. (I: that regen came from the
  pre-commit codegen-repair; a hand regen by a worker would also look like this
  and I have not read the transcript.)
- R: tommy moved bcebdc5 -> b4977c3 on 10-26 (this repo's log). The failing
  cascade run is the first bump since 10-22 that moves tommy.

### 4. The operator's environment hypothesis

- R: not contradicted, not confirmed. The lane's own docs concede an invisible
  failure path (warn + exit 0 inside a hook; fixed only at Warn level).
- R: the 10-22 success argues against a STRUCTURAL env defect in the pre-commit
  hook path (hook resolvable, nix reachable, no go.mod needed). What differs on
  10-26 is not yet known.
- I (candidates, none verified, ranked by how cheaply each can be tested):
  1. The pre-commit codegen-repair soft-failed (warn, exit 0) on the host under
     memory/disk pressure: it runs `nix eval` over all checks plus a patch
     build; flic was at 3-5 GB available and 99%-disk earlier in the week.
     Testable by reading the cascade's per-repo log for a "codegen-repair:" warn
     line (not yet found; the cascade runs on nikulin, not this host).
  2. The patch build for `tommy-codegen` failed or differed on 10-26 specifically
     because of tommy b4977c3 (x/tools v0.52.0 / x/mod / x/sync bump): e.g. the
     codegenPatch derivation builds the NEW tommy through godyn and fails offline
     or on go-stdlib, while the plain check (same tommy) is the thing that later
     reports drift. Testable: build `checks.x86_64-linux.tommy-codegen.codegenPatch`
     in a scratch cutting-garden worktree with the locked tommy at b4977c3.
  3. The hook ran but with a stale view: pre-commit restamps against the LOCK
     currently in the working tree; if the cascade committed a lock that later
     moved again (rebase in sc run's merge step onto a master with a different
     tommy-following input), the headers would be stale at the gate. Testable
     from the cascade logs and the landed vs pinned SHA.
  4. cutting-garden's `conformist-pre-commit` is not on PATH in the cascade
     session (the sweatfile comment says it "requires the devShell active");
     git then fails the hook or the hook is skipped. Testable by reading the
     sc run log; weakened by the 10-22 success unless the devshell broke since.
- I: the pre-merge build worktree identity warning ("last formatted under
  config/toolchain identity X") belongs to conformist's format cache and is
  about the pre-merge repair/format phase, not the pre-commit codegen-repair
  lane; I have found no code path where it suppresses `codegen-repair`.

### 5. Is #143's wording still the right ticket?

- R: no. #143's proposal ("run the repair lane without a go.mod") shipped as an
  opt-in tommy driver mode that was then deprecated by #144 before any consumer
  used it. The live mechanism for godyn repos is conformist's lane + igloo's
  `codegenPatch`. A follow-up on cutting-garden's stale headers belongs against
  that lane (conformist/igloo/cutting-garden or the cascade), not tommy#143.
- R: tommy#140 (header stamp degrades to `(unknown)` for tarball inputs) is still
  open and is NOT the cause here: the stale headers read `bcebdc5`, a real rev.
  Note cutting-garden consumes tommy as a locked tarball
  (`.../archive/<rev>.tar.gz`), the shape #140 is about; the stamp is correct in
  this case, so the build evidently receives the rev (I: via the locked URL or
  an override; unverified).

## Where a fix would live (provisional)

Nothing in tommy is shown to be wrong. The smallest candidate fixes, depending
on which of the inferences above is confirmed:

- soft-fail invisibility (cand. 1): conformist (make the cascade run
  `conformist codegen-repair --strict`, or surface the warning); or the cascade
  script in circus (`update-repo-in-session.bash` could run
  `conformist codegen-repair --strict` after `nix flake update`, which is
  exactly circus#398's ask; operator's earlier design constraint says
  repair must live in repos, so the preferable route is the per-repo
  `strict = true` on `linters.codegen-repair` in the cascade-exercised eval).
- patch build failing for tommy b4977c3 (cand. 2): tommy if the new tommy build
  is at fault (x/tools bump), igloo if `codegenPatch` mishandles it.
- None of these is a small tommy change as the evidence stands, so no code
  change is made in this session.

## Reproduction (V) — pending "hold lifted"

Planned, one heavy lane at a time, in a throwaway worktree of cutting-garden
(no merge, no lock bump):

1. Confirm drift exists: with the lock moved to tommy b4977c3 in the scratch
   tree only (`nix flake update tommy`), build
   `.#checks.x86_64-linux.tommy-codegen` and expect the drift failure.
2. `nix build .#checks.x86_64-linux.tommy-codegen.codegenPatch --no-link
   --print-out-paths`, read `$out/patch`: does it contain exactly the four
   header lines? (distinguishes cand. 2 from the rest)
3. `nix run .#conformist-pre-commit` / `conformist codegen-repair --list` and
   `--strict`, in (a) a plain worktree, (b) with a scrubbed environment
   (`env -i PATH=<minimal>`) as spinclass hooks run, to see whether the hook
   warns-and-exits-0 and what it prints. Record exact commands and output here.
4. Compare with the spinclass pre-merge build tree (detached worktree): is the
   tree a git worktree, is `flake.lock` dirty, is nix on PATH.

Results go below as V entries.

### V results (2026-10-26, flic, scratch copy of cutting-garden master 3904eba)

Scratch: `cp -r` of the checkout (own .git, master), then
`nix flake update --flake . tommy` (chix flake-update) moved the lock
bcebdc5 -> b4977c3 (b4977c35dbf7...) in the scratch only. Nothing was merged or
pushed anywhere.

- V: `nix build .#checks.x86_64-linux.tommy-codegen.codegenPatch` succeeded;
  its `patch` is exactly four hunks, one header line each
  (`(bcebdc5)` -> `(b4977c3)`) for internal/cgconfig, internal/config_common,
  internal/traversal_serve, pkgs/config_common — the four files named in the
  brief. => candidate 2 (patch build broken by tommy b4977c3) is REFUTED.
- V: with `flake.lock` staged (`git add`), `nix run .#conformist-pre-commit`
  (the sweatfile's hook, run from an ordinary agent session env, no devshell)
  printed `traversed 6 files / emitted 1 files for processing / formatted 0
  files (0 changed) in 3m11.099s / reformatted and restaged 4 staged file(s)`.
  Afterwards all four headers read `0.5.0 (b4977c3)` and `git status` shows
  `flake.lock` plus the four `config_tommy.go` as staged `M`. => the hook DOES
  restamp a go.nix/godyn repo from a lock-only commit; #143's goal is met by
  conformist's lane, with no go.mod in the checkout.
- V: the hook took 3m11s (cold store for the new tommy build). I: a hook or
  session timeout shorter than that, or a nix build killed under the 10-26
  memory pressure, would leave the tree unrestamped; with fail-soft this is
  silent. Candidate 1 (soft-fail/timeout under load) remains the leading
  inference; candidates 3 and 4 untested.
- NOT done: scrubbed-env (`env -i`) run and a spinclass pre-merge-tree
  comparison; the tool surface in this session has no shell, only `nix run`.
  The environment hypothesis is therefore still open, but note the plain-env
  run already shows no go/devshell dependency.

## Conclusion (provisional)

Decisive check still open: the cutting-garden step log of nikulin run
`nikulin-20261026T201701Z` (still-catalpa was asked for the `codegen-repair`
warn line).

#143 is satisfied for godyn repos by conformist's generic lane (V), not by
tommy's deprecated `--flake-attr` route (R). The 10-26 miss is not a tommy
generator or patch defect (V). Most likely a hook-time failure that the
fail-soft design hides (I): look in the cascade's cutting-garden log for a
`codegen-repair` warning or a hook timeout. Suggested owner of a fix:
conformist (surface soft failures / honour a time budget) or circus (the
cascade runs `conformist codegen-repair --strict` after `nix flake update`,
circus#398); the per-repo alternative is `linters.codegen-repair.strict = true`
in cutting-garden's `conformistCodegenEval`. No tommy change.
