# bats integration test lanes for tommy.
#
# Wraps the `batsLane` build-support helper from amarbel-llc/bats
# (`bats.lib.${system}.batsLane`) with tommy-specific defaults:
# `bats-libs` from amarbel-llc/bats on `BATS_LIB_PATH`, `TOMMY_BIN`
# exported via the `binaries` map form, `BATS_TEST_TIMEOUT`
# mirroring `zz-tests_bats/justfile`, and an offline-Go-build setup
# for `generate.bats` (tommy source + vendor/ staged into
# `stage/tommy-staged/`, GOFLAGS=-mod=vendor, GOPROXY=off).
#
# Auto-discovers `# bats file_tags=foo,bar` directives at flake-eval
# time and produces one `bats-${tag}` derivation per unique tag plus
# `bats-default` (no filter). Adding/removing tags in a `.bats` file
# invalidates the eval cache — the right behavior, but worth knowing.
#
# Only file-level tags are surfaced; per-`@test` tags are not
# auto-discovered. Use `mkBatsLane` directly for ad-hoc filters.
{
  pkgs,
  pkgs-master,
  batsLane,
  bats-libs,
  tommyBin,
  tommyFixture,
  batsSrc,
  # Generous per-test cap: the generate lanes compile Go (`go build`/`go test`
  # on the vendored synthetic module), which is slow under the CPU contention
  # of the full backend matrix building in parallel. 30s flaked under load.
  batsTestTimeout ? "120",
}:
let
  inherit (pkgs) lib;

  # Single source of truth for the per-lane builder. Callers needing
  # ad-hoc filters or alternate base derivations call this directly.
  mkBatsLane =
    {
      filter ? "",
      base ? tommyBin,
    }:
    batsLane {
      inherit base filter batsSrc;
      binaries = {
        TOMMY_BIN = {
          inherit base;
          name = "tommy";
        };
      };
      batsLibPath = [ bats-libs.batsLibPath ];
      extraEnv = {
        BATS_TEST_TIMEOUT = batsTestTimeout;
        # generate.bats reads TOMMY_FIXTURE_DIR to find the staged
        # tommy source + populated vendor/ tree. The store path is
        # injected as a derivation input (referenced through extraEnv),
        # so it's available read-only inside the sandbox; the test
        # copies vendor/ into its own writable scratch dir.
        TOMMY_FIXTURE_DIR = toString tommyFixture;
        # Synthetic downstream module in generate.bats resolves tommy +
        # its deps offline via vendor mode. GO_NO_VENDOR_CHECKS=1 lets
        # `go build -mod=vendor` work without a vendor/modules.txt —
        # mkVendorEnv doesn't generate one outside workspace mode, and
        # gomod2nix's own goConfigHook relies on this same env var.
        GOFLAGS = "-mod=vendor";
        GOPROXY = "off";
        GOSUMDB = "off";
        GO_NO_VENDOR_CHECKS = "1";
        GOTOOLCHAIN = "local";
      };
      # Go toolchain (matching buildGoApplication's go) so generate.bats
      # can invoke `go generate`, `go build`, `go test`; gofumpt so it can
      # assert generated output is gofumpt-canonical (#134).
      nativeBuildInputs = [
        pkgs-master.go
        pkgs-master.gofumpt
      ];
    };

  batsFiles = lib.filter (f: lib.hasSuffix ".bats" f) (builtins.attrNames (builtins.readDir batsSrc));

  extractFileTags =
    file:
    let
      content = builtins.readFile (batsSrc + "/${file}");
      lines = lib.splitString "\n" content;
      tagLines = lib.filter (l: lib.hasPrefix "# bats file_tags=" l) lines;
    in
    if tagLines == [ ] then
      [ ]
    else
      lib.splitString "," (lib.removePrefix "# bats file_tags=" (builtins.head tagLines));

  allFileTags = lib.unique (lib.concatMap extractFileTags batsFiles);

  batsLaneOutputs =
    lib.listToAttrs (
      map (
        tag:
        lib.nameValuePair "bats-${tag}" (mkBatsLane {
          filter = tag;
        })
      ) allFileTags
    )
    // {
      bats-default = mkBatsLane { };
    };
in
{
  inherit mkBatsLane batsLaneOutputs;
}
