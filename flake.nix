{
  description = "Tommy: a TOML library for Go";

  inputs = {
    nixpkgs.url = "github:amarbel-llc/nixpkgs";
    nixpkgs-master.url = "github:NixOS/nixpkgs/d233902339c02a9c334e7e593de68855ad26c4cb";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    bats = {
      url = "github:amarbel-llc/bats";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
    tap = {
      url = "github:amarbel-llc/tap";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
      inputs.bats.follows = "bats";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      nixpkgs-master,
      utils,
      bats,
      tap,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs-master = import nixpkgs-master { inherit system; };
        pkgs = import nixpkgs { inherit system; };

        # Shared source filter — used both by `tommyBin` (the production
        # build) and `tommyTestFixture` (staged into the bats sandbox so
        # generate.bats can `replace` tommy and build offline).
        tommySrc = pkgs.lib.cleanSourceWith {
          src = ./.;
          filter =
            path: type:
            let
              rel = pkgs.lib.removePrefix (toString ./. + "/") path;
            in
            type == "directory"
            || rel == "go.mod"
            || rel == "go.sum"
            || rel == "gomod2nix.toml"
            || pkgs.lib.hasPrefix "doc/" rel # scdoc man page sources for postInstall
            || pkgs.lib.hasSuffix ".go" rel;
        };

        # Vendor tree assembled from gomod2nix.toml. tommy has no local
        # `replace` directives in go.mod, so passing an empty replace
        # map is correct (and avoids depending on gomod2nix's internal
        # `parseGoMod`).
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
        tommyTestFixture = pkgs.runCommand "tommy-test-fixture" { } ''
          mkdir -p $out
          cp -r ${tommySrc}/. $out/
          chmod -R u+w $out
          cp -rL ${tommyVendorEnv} $out/vendor
        '';

        tommyBin = pkgs.buildGoApplication {
          pname = "tommy";
          version = "0.2.7";
          commit = self.rev or self.shortRev or "unknown";
          src = tommySrc;
          modules = ./gomod2nix.toml;
          subPackages = [ "cmd/tommy" ];

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
      in
      {
        packages = batsLib.batsLaneOutputs // {
          default = tommyBin;
          go-pkgs = tommySrc.outPath;
        };

        checks = {
          bats-default = batsLib.batsLaneOutputs.bats-default;
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
