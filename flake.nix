{
  description = "Tommy: a TOML library for Go";

  inputs = {
    igloo.url = "https://code.linenisgreat.com/igloo/archive/master.tar.gz";
    nixpkgs-master.url = "github:NixOS/nixpkgs/b4fd65b198c599cbe814fcb9f42d25d021595ec9";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    bats = {
      url = "https://code.linenisgreat.com/bats/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    tap = {
      url = "https://code.linenisgreat.com/tap/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
      inputs.bats.follows = "bats";
    };
    tap.inputs.treefmt-nix.follows = "igloo/treefmt-nix";
    tap.inputs.purse-first.inputs.conformist.follows = "conformist";
    utils.inputs.systems.follows = "igloo/systems";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";
    conformist = {
      url = "https://code.linenisgreat.com/conformist/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    bats.inputs.conformist.follows = "conformist";
  };

  outputs =
    inputs@{
      conformist,
      self,
      igloo,
      nixpkgs-master,
      utils,
      bats,
      tap,
    }:
    let
      # version.env at repo root is the single source of truth for the release
      # version. It is passed to buildGoAuto, whose backends inject it as
      # -X main.version. See eng-versioning(7).
      tommyVersion = builtins.head (
        builtins.match ".*TOMMY_VERSION=([^\n]+).*" (builtins.readFile ./version.env)
      );
    in
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs-master = import nixpkgs-master { inherit system; };
        pkgs = import igloo { inherit system; };

        # Source filtering via RFC 0001's mkGoPkgs helper. `go-pkgs`
        # excludes *_test.go and testdata/**; `go-pkgs-test` is the
        # superset used for self-consumption (tommyBin builds from this
        # so its checkPhase exercises the published artifact) and for
        # downstream consumers that want to run tommy's tests. `extras`
        # keeps doc/*.scd in both outputs so the man-page postInstall
        # can find them. See amarbel-llc/nixpkgs#42, #46.
        #
        # tommy is a go.nix producer (igloo FDR 0008): the checkout tracks no
        # go.mod or gomod2nix.toml; mkGoPkgs renders both from ./go.nix into
        # go-pkgs and go-pkgs-test, so consumers bridge tommy unchanged.
        inherit
          (pkgs.mkGoPkgs {
            src = self;
            manifest = ./go.nix;
            inherit inputs;
            extras = [ "^doc/.*\\.scd$" ];
          })
          go-pkgs
          go-pkgs-test
          ;

        # Tommy source + populated vendor/ in one tree. generate.bats
        # references this via TOMMY_FIXTURE_DIR (set by bats.nix); the
        # synthetic downstream module `replace`s tommy here and copies
        # the vendor/ into its own project tree before `go build`.
        # Uses go-pkgs (prod-shape) — the synthetic downstream module
        # doesn't need to see tommy's own test files.
        tommyTestFixture = pkgs.runCommand "tommy-test-fixture" { } ''
          mkdir -p $out
          cp -r ${go-pkgs}/. $out/
          chmod -R u+w $out
          cp -rL ${tommyBin.passthru.native.passthru.vendorEnv} $out/vendor
        '';

        # shortRev when the tree is clean; dirtyShortRev ("<sha>-dirty") when
        # it isn't — so a dirty build is distinguishable in `tommy version` and
        # the generated-file header (#125). Flakes expose neither on a non-git
        # build, hence the "unknown" fallback.
        tommyCommit = self.shortRev or self.dirtyShortRev or "unknown";

        # tommy's Go build via igloo's buildGoAuto: godyn (per-package,
        # content-addressed) on igloo's godynSystems, buildGoApplication
        # elsewhere. Both stay reachable as passthru.native / passthru.bga, and
        # gates key off passthru.backend rather than a system name (godyn(7)).
        # A go.nix producer self-consumes its go-pkgs-test from the gomod2nix.toml
        # mkGoPkgs rendered into it (godyn(7) § Producers), not from `manifest`:
        # go-pkgs-test carries that rendered go.mod, which a manifest build
        # rejects. The package graph is derived at eval time, so none is committed.
        tommyBin =
          (pkgs.buildGoAuto {
            pname = "tommy";
            version = tommyVersion;
            src = go-pkgs-test;
            modules = "${go-pkgs-test}/gomod2nix.toml";
            subPackages = [ "cmd/tommy" ];

            # commit has no buildGoAuto slot, so it rides both backends' args.
            # godyn also derives the test graph, for tommyGoTests below.
            nativeArgs = {
              commit = tommyCommit;
              tests = true;
            };
            bgaArgs = {
              commit = tommyCommit;
              # Skips ./generate/... — those tests scaffold synthetic Go
              # modules and call go/packages.Load, which needs network or a
              # pre-populated module cache that the nix sandbox doesn't have.
              # The go-generate check and the bats lanes cover the generator.
              doCheck = true;
              checkPhase = ''
                runHook preCheck
                go test -p $NIX_BUILD_CORES ./pkg/... ./internal/...
                runHook postCheck
              '';
            };

            nativeBuildInputs = [ pkgs.scdoc ];

            postInstall = ''
              tmp=$(mktemp)
              for f in doc/*.1.scd; do
                [ -e "$f" ] || continue
                name=$(basename "$f" .scd)
                scdoc < "$f" > "$tmp"
                install -Dm644 "$tmp" "$out/share/man/man1/$name"
              done
              for f in doc/*.7.scd; do
                [ -e "$f" ] || continue
                name=$(basename "$f" .scd)
                scdoc < "$f" > "$tmp"
                install -Dm644 "$tmp" "$out/share/man/man7/$name"
              done
              rm -f "$tmp"
            '';
          }).overrideAttrs
            (old: {
              meta = (old.meta or { }) // {
                description = "A TOML library for Go";
                homepage = "https://code.linenisgreat.com/tommy";
                license = pkgs.lib.licenses.mit;
                mainProgram = "tommy";
              };
            });

        # tommy built straight from go.nix (src = the checkout, which tracks no
        # go.mod). The escape hatch's target: `just update-go-deps` runs godyn-go
        # against it, whose ingest needs the manifest tommyBin (built from the
        # rendered toml) does not carry.
        tommyGoNix = pkgs.buildGodynModule {
          pname = "tommy";
          version = tommyVersion;
          commit = tommyCommit;
          src = self;
          manifest = ./go.nix;
          subPackages = [ "cmd/tommy" ];
        };

        # godyn's per-package go test lane, scoped to ./pkg and ./internal — what
        # the bga checkPhase runs. ./generate is left out (its tests need a Go
        # module cache; see the go-generate check), and runs this manifest doesn't
        # reference are never built.
        tommyGoTests =
          let
            modPath = "code.linenisgreat.com/tommy";
            inScope =
              ip: _:
              builtins.any (dir: ip == "${modPath}/${dir}" || pkgs.lib.hasPrefix "${modPath}/${dir}/" ip) [
                "pkg"
                "internal"
              ];
            runs = pkgs.lib.filterAttrs inScope tommyBin.passthru.tests;
          in
          pkgs.runCommandLocal "tommy-go-tests" { } (
            ": > $out\n"
            + pkgs.lib.concatMapStringsSep "\n" (run: "cat ${run}/result >> $out") (pkgs.lib.attrValues runs)
          );

        # Filter zz-tests_bats so lane store paths only change when
        # actual test inputs change — not on unrelated repo edits. The
        # local `justfile` is excluded; lanes invoke bats directly, not
        # through `just`.
        batsSrc = pkgs.lib.cleanSourceWith {
          src = ./zz-tests_bats;
          filter =
            path: type:
            let
              bn = builtins.baseNameOf path;
            in
            type == "directory"
            || pkgs.lib.hasSuffix ".bats" bn
            || bn == "common.bash"
            || bn == "setup_suite.bash";
        };

        batsLib = import ./bats.nix {
          inherit pkgs pkgs-master batsSrc;
          batsLane = bats.lib.${system}.batsLane;
          bats-libs = bats.packages.${system}.bats-libs;
          inherit tommyBin;
          tommyFixture = tommyTestFixture;
        };

        # Offline Go module cache for the go-generate check below. The
        # ./generate integration tests scaffold synthetic Go modules at
        # runtime and resolve tommy + its deps via go/packages.Load and
        # `go build`/`go test`; with this cache + GOFLAGS=-mod=mod +
        # GOPROXY=off they resolve without network (the bats lanes use vendor
        # mode for the same reason). Fixed-output (network at build,
        # hash-pinned); volatile lock/sumdb bits are stripped so the recursive
        # output hash is stable. See #83.
        goModCache = pkgs-master.stdenvNoCC.mkDerivation {
          name = "tommy-go-modcache";
          src = go-pkgs-test;
          nativeBuildInputs = [ pkgs-master.go ];
          buildPhase = ''
            export HOME=$TMPDIR
            export GOPATH=$TMPDIR/gopath
            export GOMODCACHE=$out
            export GOFLAGS=-mod=mod
            export GOTOOLCHAIN=local
            mkdir -p $out
            go mod download all
            rm -rf $out/cache/lock $out/cache/download/sumdb
          '';
          dontInstall = true;
          dontFixup = true;
          outputHashMode = "recursive";
          outputHashAlgo = "sha256";
          outputHash = "sha256-2VUHE0FE/062q5QHrgrSrpU3IEAMuANEn5nAQILPkeI=";
        };

        # The ./generate suite builds tommy in -mod=mod, so its module root needs a
        # go.sum; a go.nix checkout has none (FDR 0008). Record it offline from
        # goModCache and add it to that suite's source only.
        goGenerateSrc =
          pkgs-master.runCommand "tommy-go-generate-src" { nativeBuildInputs = [ pkgs-master.go ]; }
            ''
              cp -r --no-preserve=mode ${go-pkgs-test} $out
              cp -r --no-preserve=mode ${goModCache} $TMPDIR/modcache
              cd $out
              HOME=$TMPDIR GOPATH=$TMPDIR/gopath GOCACHE=$TMPDIR/gocache \
                GOMODCACHE=$TMPDIR/modcache GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off \
                GOTOOLCHAIN=local go mod download all
              test -s go.sum
            '';

        # A godyn test run of ./generate, the rich integration suite (incl. the
        # #81/#82 regression tests) the bats matrix's breadth doesn't reach. Its
        # tests scaffold synthetic modules that `replace` tommy with the module
        # root (`..` of the package) and build ./cmd/tommy there, so the run tree
        # carries the module files those builds read. They shell out to go
        # offline against goModCache (TOMMY_TEST_OFFLINE), so each run stages a
        # writable copy of it. Instances differing only in testEnv/testFlags share
        # the test binary (content-addressed) and repeat just the run. See #83.
        tommyGenerateRun =
          {
            testEnv ? { },
            testFlags ? [ ],
          }:
          (pkgs.buildGodynModule {
            pname = "tommy";
            version = tommyVersion;
            commit = tommyCommit;
            src = goGenerateSrc;
            modules = "${goGenerateSrc}/gomod2nix.toml";
            tests = true;
            nativeCheckInputs = [ pkgs-master.go ];
            testFiles.generate = [
              "go.mod"
              "go.sum"
              "cmd"
              "internal"
              "pkg"
            ];
            testEnv = {
              GOFLAGS = "-mod=mod";
              GOPROXY = "off";
              GOSUMDB = "off";
              GOTOOLCHAIN = "local";
              TOMMY_TEST_OFFLINE = "1";
            }
            // testEnv;
            testPreRun = ''
              export HOME=$TMPDIR GOPATH=$TMPDIR/gopath GOCACHE=$TMPDIR/gocache
              cp -r --no-preserve=mode ${goModCache} $TMPDIR/modcache
              export GOMODCACHE=$TMPDIR/modcache
            '';
            inherit testFlags;
          }).passthru.tests."code.linenisgreat.com/tommy/generate";

        goGenerateCheck = tommyGenerateRun { };

        # Multi-seed fuzz sweep. The go-generate check above runs the three
        # generative fuzzers (TestRoundTripFuzz, TestRoundTripFuzzDelegation,
        # TestRoundTripSpellingFuzz) at seed 1 only; this check loops the seed so
        # CI fuzzes many random type-shape sets per merge, not just seed 1 —
        # catching codegen/decoder bugs in shape combinations seed 1 misses (the
        # #105/#107/#108 class). Seed count is a build-time constant here; for
        # ad-hoc local widening past it use the network-mode debug-fuzz-*-sweep
        # just recipes (which take an n= arg).
        fuzzSweepSeeds = 10;
        goFuzzSweep = pkgs.runCommandLocal "tommy-fuzz-sweep" { } (
          ": > $out\n"
          + pkgs.lib.concatMapStringsSep "\n" (
            s:
            let
              run = tommyGenerateRun {
                testEnv.TOMMY_FUZZ_SEED = toString s;
                testFlags = [
                  "-test.run=^TestRoundTrip"
                  "-test.count=1"
                ];
              };
            in
            "echo 'seed ${toString s}:' >> $out; cat ${run}/result >> $out"
          ) (pkgs.lib.range 1 fuzzSweepSeeds)
        );

        # conformist integration, owned by tommy so the consuming flake resolves
        # which tommy backs it (no per-repo driver duplication or separate
        # binary pinning). THIS flake's tommyBin is baked into the codegen
        # driver's PATH, so a consumer that pins `tommy` as a flake input gets a
        # driver whose `tommy` matches the library their `//go:generate tommy
        # generate` codegen was produced with.
        #
        # The driver walks the tree for `//go:generate tommy generate`
        # directives and runs `tommy generate --check` (check) / `tommy generate`
        # (repair) per file, mirroring `go generate` but scoped to tommy.
        #
        # `go` comes from the caller's PATH when there is one — inside igloo's
        # codegenCheck or goRun (a go.nix module, FDR 0008) that is the module's
        # own toolchain, which must satisfy its go directive under
        # GOTOOLCHAIN=local — and falls back to the pinned pkgs-master.go only
        # when none is on PATH, so the lane still works in a BARE env: spinclass's
        # git pre-commit / pre-merge hooks run the repair command with a plain
        # os.Environ() (no devShell / direnv). An earlier version skipped (exit 0)
        # without go; that silently no-op'd the repair lane in exactly those bare
        # hooks, letting codegen drift survive to the merge gate (tommy#138).
        #
        # Without an enclosing go.mod (a go.nix module's checkout) `tommy
        # generate` fails per file naming codegenCheck and godyn-go; vendor/ (the
        # codegenCheck tree) is never walked. Such a module passes
        # `--flake-attr <attr>` instead (tommy#143): the driver then never runs
        # the generator in the checkout. It builds `.#<attr>` — the module's
        # goNixCodegen `repair`, the same vendored tree its `check` runs in — and
        # applies the patch (repair) or fails on a non-empty one (--check). `nix`
        # and `git`, like `go`, come from PATH when present, else the pinned ones.
        #
        # Either go is safe because `tommy generate`'s output is NOT
        # sensitive to the go *toolchain* version: rendering and gofmt/gofumpt
        # are libraries compiled into tommyBin, and gofumpt's LangVersion is read
        # from the consumer module's go.mod on disk (detectGoLangVersion), not
        # the toolchain. The ambient go was only ever used by go/packages
        # (`go list`) for type metadata, which is version-insensitive here.
        conformistTommyCodegen = pkgs.writeShellApplication {
          name = "conformist-tommy-codegen";
          runtimeInputs = [
            tommyBin
            pkgs.coreutils
            pkgs.findutils
            pkgs.gnugrep
          ];
          text = ''
            usage() {
              echo "usage: conformist-tommy-codegen [--check] [--flake-attr <attr>]" >&2
            }
            check=0
            flake_attr=""
            while [ "$#" -gt 0 ]; do
              case "$1" in
                --check) check=1 ;;
                --flake-attr)
                  [ "$#" -ge 2 ] || { usage; exit 2; }
                  flake_attr=$2
                  shift
                  ;;
                --flake-attr=*) flake_attr=''${1#--flake-attr=} ;;
                *) usage; exit 2 ;;
              esac
              shift
            done

            if [ -n "$flake_attr" ]; then
              command -v nix >/dev/null 2>&1 || PATH="$PATH:${pkgs.nix}/bin"
              command -v git >/dev/null 2>&1 || PATH="$PATH:${pkgs.git}/bin"
              out=$(nix build --no-link --print-out-paths ".#$flake_attr")
              patch="$out/patch"
              if [ ! -s "$patch" ]; then
                exit 0
              fi
              if [ "$check" -eq 1 ]; then
                cat "$patch" >&2
                echo "tommy-codegen: generated files are stale (the patch above, from .#$flake_attr); run the repair" >&2
                exit 1
              fi
              # the patch is relative to the module root, the tree root conformist runs in
              apply_args=(-p2)
              prefix=$(git rev-parse --show-prefix)
              [ -z "$prefix" ] || apply_args+=("--directory=$prefix")
              # all or nothing: a hunk that fails leaves the checkout untouched
              git apply "''${apply_args[@]}" "$patch" \
                || { echo "tommy-codegen: $patch does not apply to the checkout; nothing written" >&2; exit 1; }
              echo "tommy-codegen: applied $patch" >&2
              exit 0
            fi

            command -v go >/dev/null 2>&1 || PATH="$PATH:${pkgs-master.go}/bin"
            gen_args=(generate)
            if [ "$check" -eq 1 ]; then
              gen_args+=(--check)
            fi
            if ! command -v tommy >/dev/null 2>&1; then
              echo "tommy-codegen: tommy not on PATH; skipping" >&2
              exit 0
            fi
            status=0
            while IFS= read -r f; do
              dir=$(dirname "$f")
              base=$(basename "$f")
              ( cd "$dir" || exit 1; GOFILE="$base" tommy "''${gen_args[@]}"; ) || status=1
            done < <(grep -rIl --include='*.go' --exclude-dir=vendor 'go:generate tommy generate' . 2>/dev/null | grep -v '/result' || true)
            exit "$status"
          '';
        };

        # tommy codegen for a go.nix module (igloo FDR 0008), which has no go.mod
        # in its checkout, as a check/repair pair that resolves the module
        # identically (tommy#143). `check` is the module's own
        # passthru.codegenCheck: `command` runs in the vendored module tree (go.mod
        # rendered from go.nix, offline) and any diff from src fails. `repair` is
        # igloo's passthru.codegenPatch (igloo#80): the same check's tree and
        # command, offline and cached, writing the diff to $out/patch (`git apply
        # -p2`, empty when current) instead of failing — so applying the patch
        # makes `check` pass by construction, and check/repair can never diverge
        # (codegenCheck is itself defined as "codegenPatch is empty"). The repair
        # lane (conformist-tommy-codegen --flake-attr) builds `repair` and applies
        # the patch to the checkout, so the generator never runs outside nix.
        goNixCodegen =
          {
            module,
            command ? "go generate -run tommy ./...",
            nativeBuildInputs ? [ ],
            exclude ? [ ],
          }:
          let
            check = module.passthru.codegenCheck {
              inherit command exclude;
              nativeBuildInputs = [ tommyBin ] ++ nativeBuildInputs;
            };
          in
          {
            inherit check;
            repair = check.passthru.codegenPatch;
          };

        # A go.nix module (igloo FDR 0008) with no go.mod in its tree. The
        # codegen-go-nix checks run tommy codegen where such a module runs it —
        # igloo's passthru.codegenCheck: the go.mod rendered from go.nix, vendored
        # deps, offline — via `go generate` (the merge-gate shape) and via the
        # conformist repair driver, and build the result. config_tommy.go is not
        # committed (exclude), so no golden file tracks tommy's build stamp.
        # codegen-go-nix-no-gomod asserts the actionable failure outside nix.
        codegenGoNixSrc = ./zz-tests_nix/testdata/codegen-go-nix;
        codegenGoNixModule =
          src:
          pkgs.buildGodynModule {
            pname = "tommy-codegen-go-nix";
            version = "0.6.0";
            inherit src;
            manifest = src + "/go.nix";
            goFlakeInputOverrides."code.linenisgreat.com/tommy".src = go-pkgs;
          };
        codegenGoNix = codegenGoNixModule codegenGoNixSrc;

        # The goNixCodegen check/repair pair against a stale committed companion
        # (tommy#143): the fixture with config_tommy.go committed under a header
        # rev no build carries (0000000) — what a tommy bump leaves behind.
        # `stale.check` must fail, and `stale.repair`'s patch must restamp it to
        # exactly what the generator writes into the companion-less fixture
        # (`fresh`) — the content `check` regenerates and compares against, since
        # tommy generate overwrites the companion rather than reading it. A module
        # over a derivation-built src is out: godyn reads its src at eval time,
        # which a CA placeholder path forbids, so the stale tree is committed.
        applyPatch =
          name: src: patch:
          pkgs.runCommandLocal name { nativeBuildInputs = [ pkgs.git ]; } ''
            cp -r --no-preserve=mode ${src} $out
            cd $out
            [ ! -s ${patch} ] || git apply -p2 ${patch}
          '';
        codegenGoNixFresh =
          applyPatch "tommy-codegen-go-nix-fresh" codegenGoNixSrc
            "${(goNixCodegen { module = codegenGoNix; }).repair}/patch";
        codegenGoNixStaleSrc = ./zz-tests_nix/testdata/codegen-go-nix-stale;
        codegenGoNixStale = goNixCodegen { module = codegenGoNixModule codegenGoNixStaleSrc; };
        codegenGoNixRepairedSrc =
          applyPatch "tommy-codegen-go-nix-repaired" codegenGoNixStaleSrc
            "${codegenGoNixStale.repair}/patch";

        # The repair lane end to end under conformist's real `--staged` pre-commit
        # hook, built from tommy's conformistModule with tommy.codegen.flakeAttr:
        # a lock-only commit (the tommy-bump shape) in a checkout holding the stale
        # companion must restamp it and stage the result. `nix` cannot run inside
        # the sandbox, so a stub standing in for `nix build .#<attr>` hands the
        # driver the already-built stale repair (the driver prefers a nix on PATH).
        codegenPrecommitEval = conformist.lib.evalModule pkgs {
          imports = [ conformistModule ];
          package = conformistPkg;
          projectRootFile = "flake.nix";
          tommy.codegen.flakeAttr = "tommy-codegen-repair";
        };
        codegenPrecommitNixStub = pkgs.writeShellScriptBin "nix" ''
          case "$*" in
            "build "*" .#tommy-codegen-repair"*) echo ${codegenGoNixStale.repair} ;;
            *) echo "nix stub: unexpected invocation: nix $*" >&2; exit 1 ;;
          esac
        '';
        codegenGoNixCheck =
          command: tools:
          codegenGoNix.passthru.codegenCheck {
            command = "${command} && test -s config_tommy.go && go build ./...";
            nativeBuildInputs = tools;
            exclude = [ "config_tommy.go" ];
          };
        codegenGoNixChecks = {
          codegen-go-nix = codegenGoNixCheck "go generate ./..." [ tommyBin ];
          codegen-go-nix-repair = codegenGoNixCheck "conformist-tommy-codegen" [ conformistTommyCodegen ];
          codegen-go-nix-no-gomod =
            pkgs.runCommandLocal "tommy-codegen-go-nix-no-gomod"
              {
                nativeBuildInputs = [ conformistTommyCodegen ];
              }
              ''
                cp -r --no-preserve=mode ${codegenGoNixSrc} work
                cd work
                if conformist-tommy-codegen 2> err; then
                  echo "conformist-tommy-codegen succeeded without a go.mod" >&2
                  exit 1
                fi
                grep -q 'no go.mod' err || { cat err >&2; exit 1; }
                touch $out
              '';
          codegen-go-nix-stale-detected =
            pkgs.runCommandLocal "tommy-codegen-go-nix-stale-detected"
              {
                failed = pkgs.testers.testBuildFailure codegenGoNixStale.check;
              }
              ''
                grep -q 'godyn codegen drift' $failed/testBuildFailure.log
                grep -q '(0000000)' $failed/testBuildFailure.log
                touch $out
              '';
          codegen-go-nix-repair-roundtrip =
            pkgs.runCommandLocal "tommy-codegen-go-nix-repair-roundtrip"
              {
                patch = "${codegenGoNixStale.repair}/patch";
              }
              ''
                [ -s "$patch" ] || { echo "stale companion produced no repair patch" >&2; exit 1; }
                [ "$(grep '^diff --git' "$patch")" = "diff --git a/src/config_tommy.go b/work/config_tommy.go" ] \
                  || { cat "$patch" >&2; exit 1; }
                grep -q '^-// Code generated by tommy .* (0000000); DO NOT EDIT\.$' "$patch"
                grep -q '^+// Code generated by tommy .* (${tommyCommit}); DO NOT EDIT\.$' "$patch"
                cmp ${codegenGoNixRepairedSrc}/config_tommy.go ${codegenGoNixFresh}/config_tommy.go
                touch $out
              '';
          codegen-go-nix-precommit =
            pkgs.runCommandLocal "tommy-codegen-go-nix-precommit"
              {
                nativeBuildInputs = [
                  pkgs.git
                  codegenPrecommitNixStub
                ];
              }
              ''
                export HOME=$TMPDIR
                cp -r --no-preserve=mode ${codegenGoNixStaleSrc} repo
                cd repo
                touch flake.nix # the tree root marker; the stub stands in for its eval
                git init -q
                git config user.email tommy@example.com
                git config user.name tommy
                git add -A
                git commit -qm stale
                echo '{}' > flake.lock
                git add flake.lock
                ${pkgs.lib.getExe codegenPrecommitEval.config.build.preCommit}
                git diff --cached --name-only | grep -qx config_tommy.go \
                  || { echo "the restamped companion was not staged" >&2; git status >&2; exit 1; }
                git diff --quiet -- config_tommy.go \
                  || { echo "the worktree companion differs from the staged one" >&2; exit 1; }
                git show :config_tommy.go | cmp - ${codegenGoNixRepairedSrc}/config_tommy.go
                touch $out
              '';
        };

        # A conformist Nix module wiring both halves of the integration using
        # THIS flake's tommy. Import it into a `conformist.lib.evalModule` /
        # `submoduleWith` module list and the generated config gains a
        # tommy-backed [formatter.tommy] + [linter.tommy-codegen] with no further
        # wiring — the flake resolves the binary. Emits raw `settings.*` (the
        # freeform surface) rather than toggling conformist's own
        # programs.tommy / linters.tommy-codegen, so it composes regardless of
        # whether those generic modules are also present.
        #
        # The codegen CHECK is a no-op `true` (so `conformist check` never
        # reports false drift): `tommy generate` needs the go toolchain
        # (go/packages) that a sandboxed conformist check lane lacks, so it
        # cannot run as a check there. Since #134 tommy gofumpt's its own output
        # (version-matched to the consumer's go.mod), the generated code is
        # gofumpt-canonical — so enforcing drift, if a consumer wants it, belongs
        # in a go-available lane (`tommy generate` + `git diff`), not here. The
        # REPAIR command regenerates, so codegen lands in `conformist --commit`
        # and, restaged, in the `--staged` pre-commit hook.
        #
        # A go.nix module (no go.mod) sets tommy.codegen.flakeAttr to the flake
        # package holding its goNixCodegen `repair`; the repair command then
        # builds that and applies its patch instead of running `tommy generate`
        # in the checkout (tommy#143).
        conformistModule =
          { lib, config, ... }:
          let
            cfg = config.tommy.codegen;
          in
          {
            options.tommy.codegen.flakeAttr = lib.mkOption {
              type = lib.types.nullOr lib.types.str;
              default = null;
              example = "tommy-codegen-repair";
              description = ''
                Flake attribute (resolved from the tree root, e.g. `packages.<system>.<name>`
                via `.#<name>`) of a go.nix module's `tommy.lib.<system>.goNixCodegen`
                repair. When set, the tommy-codegen repair builds it in nix and applies
                its patch to the checkout; null runs `tommy generate` per file, which
                needs a go.mod.
              '';
            };
            config.settings.formatter.tommy = {
              command = lib.getExe tommyBin;
              options = [ "fmt" ];
              includes = [ "*.toml" ];
            };
            config.settings.linter.tommy-codegen = {
              command = "true";
              "repair-command" = lib.getExe conformistTommyCodegen;
              "repair-options" = lib.optionals (cfg.flakeAttr != null) [
                "--flake-attr"
                cfg.flakeAttr
              ];
              # A trigger gate only (passes-files = false): the repair walks the
              # whole tree whenever one of these is staged. `*.go` covers every
              # depth (conformist compiles globs via gobwas/glob.Compile with no
              # separator, so `*` crosses `/`). flake.lock is there because a
              # tommy bump restamps every generated header (tommy#125) from a
              # lock-only commit that stages no .go; go.nix because its go
              # directive sets gofumpt's language version.
              includes = [
                "*.go"
                "flake.lock"
                "go.nix"
              ];
              passes-files = false;
              # stage what the repair writes, so the commit carries the regen:
              # modified companions, brand-new ones, and removed ones
              "restage-repair-outputs" = true;
              "stage-new-outputs" = true;
              "stage-deleted-outputs" = true;
            };
          };

        conformistPkg = conformist.packages.${system}.default;

        eval = conformist.lib.evalModule pkgs {
          imports = [
            conformist.lib.presets.eng
            conformist.lib.presets.eng-go
            ./conformist.nix
          ];
          package = conformistPkg;
        };

        impureEval = conformist.lib.evalModule pkgs {
          imports = [ conformist.lib.presets.eng-impure ];
          package = conformistPkg;
          projectRootFile = "flake.nix";
        };
      in
      {
        packages = batsLib.batsLaneOutputs // {
          default = tommyBin;
          conformist-tommy-codegen = conformistTommyCodegen;
          tommy-gonix = tommyGoNix;
          inherit go-pkgs go-pkgs-test;
          go-generate = goGenerateCheck;
          fuzz-sweep = goFuzzSweep;
          conformist-impure-config = impureEval.config.build.configFile;
          conformist-pre-commit = eval.config.build.preCommit;
          conformist-repair = eval.config.build.repair;
        };

        # conformist Nix module (per-system because it bakes this system's
        # tommyBin + driver). Consumers: `imports = [ tommy.conformistModule.${system} ];`.
        inherit conformistModule;

        # goNixCodegen { module; command?; nativeBuildInputs?; exclude? } — the
        # codegen check/repair pair for a go.nix module (tommy#143). Consumers:
        # `tommy.lib.${system}.goNixCodegen { module = myapp; }`.
        lib = { inherit goNixCodegen; };

        # Every bats lane is a check, so `nix flake check` (the merge-hook
        # `just validate`) runs the full matrix: each file_tag lane plus the
        # generate lane under all four codegen backends (jen/api/cst/legacy).
        # Backend divergence (e.g. #82) now fails CI rather than slipping
        # through on the default backend. See #83.
        checks =
          batsLib.batsLaneOutputs
          // {
            formatting = eval.config.build.check self;
          }
          // codegenGoNixChecks
          # godyn's per-package tests, vet and lint. On the bga backend the unit
          # tests run in tommyBin's checkPhase instead, which the bats lanes build.
          // pkgs.lib.optionalAttrs (tommyBin.passthru.backend == "native") {
            go-tests = tommyGoTests;
            go-generate = goGenerateCheck;
            fuzz-sweep = goFuzzSweep;
            go-vet = tommyBin.passthru.vetAll;
            go-lint = pkgs.buildGodynLint {
              pname = "tommy";
              version = tommyVersion;
              commit = tommyCommit;
              src = go-pkgs-test;
              modules = "${go-pkgs-test}/gomod2nix.toml";
            };
          };

        devShells.default = pkgs-master.mkShell {
          packages = [
            # go.nix escape hatch and inner test loop (FDR 0008); no ambient go.
            pkgs.godyn-go
            pkgs.godyn-test
            pkgs-master.gopls
            pkgs-master.gotools
            pkgs-master.golangci-lint
            pkgs-master.delve
            pkgs-master.gofumpt
            pkgs.just
            bats.packages.${system}.bats
            bats.packages.${system}.batman
            tap.packages.${system}.tap-dancer
            conformistPkg
            eval.config.build.preCommit
            eval.config.build.repair
          ];
        };

        formatter = eval.config.build.wrapper;
      }
    );
}
