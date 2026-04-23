{
  description = "Tommy: a TOML library for Go";

  inputs = {
    nixpkgs.url = "github:amarbel-llc/nixpkgs";
    nixpkgs-master.url = "github:NixOS/nixpkgs/e2dde111aea2c0699531dc616112a96cd55ab8b5";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    bob = {
      url = "github:amarbel-llc/bob";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      nixpkgs-master,
      utils,
      bob,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs-master = import nixpkgs-master { inherit system; };
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages = {
          default = pkgs.buildGoApplication {
            pname = "tommy";
            version = "0.2.3";
            commit = self.rev or self.shortRev or "unknown";
            src = pkgs.lib.cleanSourceWith {
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
            bob.packages.${system}.batman
            bob.packages.${system}.tap-dancer
          ];
        };
      }
    );
}
