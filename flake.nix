{
  description = "Tommy: a TOML library for Go";

  inputs = {
    igloo.url = "github:amarbel-llc/igloo";
    nixpkgs-master.url = "github:NixOS/nixpkgs/d233902339c02a9c334e7e593de68855ad26c4cb";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    bats = {
      url = "github:amarbel-llc/bats";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    tap = {
      url = "github:amarbel-llc/tap";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
      inputs.bats.follows = "bats";
    };
  };

  outputs =
    {
      self,
      igloo,
      nixpkgs-master,
      utils,
      bats,
      tap,
    }:
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
        inherit (pkgs.mkGoPkgs {
          src = self;
          extras = [ "^doc/.*\\.scd$" ];
        }) go-pkgs go-pkgs-test;

        # Vendor tree assembled from gomod2nix.toml for the offline
        # bats fixture below. tommy has no local `replace` directives
        # in go.mod, so an empty replace map is correct (and avoids
        # depending on gomod2nix's internal `parseGoMod`).
        tommyVendorEnv = pkgs.mkVendorEnv {
          go = pkgs-master.go;
          modulesStruct = builtins.fromTOML (builtins.readFile ./gomod2nix.toml);
          goMod = { replace = { }; };
          pwd = ./.;
        };

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
          cp -rL ${tommyVendorEnv} $out/vendor
        '';

        tommyBin = pkgs.buildGoApplication {
          pname = "tommy";
          version = "0.3.2";
          commit = self.rev or self.shortRev or "unknown";
          src = go-pkgs-test;
          modules = ./gomod2nix.toml;
          subPackages = [ "cmd/tommy" ];
          # Skips ./generate/... — those tests scaffold synthetic Go
          # modules and call go/packages.Load, which needs network or a
          # pre-populated module cache that the nix sandbox doesn't have.
          # The bats lane covers the generator end-to-end against the
          # installed binary.
          doCheck = true;
          checkPhase = ''
            runHook preCheck
            go test -p $NIX_BUILD_CORES ./pkg/... ./internal/...
            runHook postCheck
          '';

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

          meta = {
            description = "A TOML library for Go";
            homepage = "https://github.com/amarbel-llc/tommy";
            license = pkgs.lib.licenses.mit;
          };
        };

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
          outputHash = "sha256-9MbbXy7F/Qy8NymMrtiLquhUDdVB2HY/8JrZNeRH18o=";
        };

        # Runs the rich Go ./generate integration suite (incl. the #81/#82
        # regression tests) offline on the default (jen) backend — the depth
        # the bats matrix's breadth doesn't reach. Builds from go-pkgs-test
        # (the test-inclusive source) with TOMMY_TEST_OFFLINE so the synthetic
        # modules resolve from goModCache without network. See #83.
        goGenerateCheck = pkgs-master.runCommand "tommy-go-generate" {
          nativeBuildInputs = [ pkgs-master.go ];
        } ''
          export HOME=$TMPDIR
          export GOPATH=$TMPDIR/gopath
          export GOCACHE=$TMPDIR/gocache
          cp -r --no-preserve=mode ${goModCache} $TMPDIR/modcache
          export GOMODCACHE=$TMPDIR/modcache
          export GOFLAGS=-mod=mod
          export GOPROXY=off
          export GOSUMDB=off
          export GOTOOLCHAIN=local
          export TOMMY_TEST_OFFLINE=1
          cp -r --no-preserve=mode ${go-pkgs-test} ./src
          cd ./src
          go test ./generate/...
          touch $out
        '';

        # Multi-seed fuzz sweep. The go-generate check above runs the three
        # generative fuzzers (TestRoundTripFuzz, TestRoundTripFuzzDelegation,
        # TestRoundTripSpellingFuzz) at seed 1 only; this check loops the seed so
        # CI fuzzes many random type-shape sets per merge, not just seed 1 —
        # catching codegen/decoder bugs in shape combinations seed 1 misses (the
        # #105/#107/#108 class). Same offline env as go-generate. Seed count is a
        # build-time constant here; for ad-hoc local widening past it use the
        # network-mode debug-fuzz-*-sweep just recipes (which take an n= arg).
        fuzzSweepSeeds = 10;
        goFuzzSweep = pkgs-master.runCommand "tommy-fuzz-sweep" {
          nativeBuildInputs = [ pkgs-master.go ];
        } ''
          export HOME=$TMPDIR
          export GOPATH=$TMPDIR/gopath
          export GOCACHE=$TMPDIR/gocache
          cp -r --no-preserve=mode ${goModCache} $TMPDIR/modcache
          export GOMODCACHE=$TMPDIR/modcache
          export GOFLAGS=-mod=mod
          export GOPROXY=off
          export GOSUMDB=off
          export GOTOOLCHAIN=local
          export TOMMY_TEST_OFFLINE=1
          cp -r --no-preserve=mode ${go-pkgs-test} ./src
          cd ./src
          for s in $(seq 1 ${toString fuzzSweepSeeds}); do
            echo "=== fuzz seed $s ==="
            TOMMY_FUZZ_SEED=$s go test -run '^TestRoundTrip' ./generate/ -count=1
          done
          touch $out
        '';
      in
      {
        packages = batsLib.batsLaneOutputs // {
          default = tommyBin;
          inherit go-pkgs go-pkgs-test;
          go-generate = goGenerateCheck;
          fuzz-sweep = goFuzzSweep;
        };

        # Every bats lane is a check, so `nix flake check` (the merge-hook
        # `just validate`) runs the full matrix: each file_tag lane plus the
        # generate lane under all four codegen backends (jen/api/cst/legacy).
        # Backend divergence (e.g. #82) now fails CI rather than slipping
        # through on the default backend. See #83.
        checks = batsLib.batsLaneOutputs // {
          go-generate = goGenerateCheck;
          fuzz-sweep = goFuzzSweep;
        };

        devShells.default = pkgs-master.mkShell {
          packages = [
            (pkgs.mkGoEnv { pwd = ./.; })
            pkgs-master.gopls
            pkgs-master.gotools
            pkgs-master.golangci-lint
            pkgs-master.delve
            pkgs-master.gofumpt
            pkgs.just
            bats.packages.${system}.bats
            bats.packages.${system}.batman
            tap.packages.${system}.tap-dancer
          ];
        };
      }
    );
}
