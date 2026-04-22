{
  description = "Tommy: a TOML library for Go";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/4590696c8693fea477850fe379a01544293ca4e2";
    nixpkgs-master.url = "github:NixOS/nixpkgs/e2dde111aea2c0699531dc616112a96cd55ab8b5";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-utils.follows = "utils";
    };
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
      gomod2nix,
      bob,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs-master = import nixpkgs-master { inherit system; };
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            gomod2nix.overlays.default
            (_: _: { go = pkgs-master.go; })
          ];
        };
      in
      {
        packages = {
          default = pkgs.buildGoApplication {
            pname = "tommy";
            version = "0.1.0";
            src = ./.;
            modules = ./gomod2nix.toml;
            subPackages = [ "cmd/tommy" ];

            nativeBuildInputs = [ pkgs.scdoc ];

            postInstall = ''
              for f in doc/*.1.scd; do
                [ -e "$f" ] || continue
                name=$(basename "$f" .scd)
                install -Dm644 /dev/stdin "$out/share/man/man1/$name" < <(scdoc < "$f")
              done
              for f in doc/*.7.scd; do
                [ -e "$f" ] || continue
                name=$(basename "$f" .scd)
                install -Dm644 /dev/stdin "$out/share/man/man7/$name" < <(scdoc < "$f")
              done
            '';

            meta = {
              description = "A TOML library for Go";
              homepage = "https://github.com/amarbel-llc/tommy";
              license = pkgs.lib.licenses.mit;
            };
          };
        };

        devShells.default = pkgs.mkShell {
          packages = [
            pkgs-master.go
            pkgs-master.gopls
            pkgs-master.gotools
            pkgs-master.golangci-lint
            pkgs-master.delve
            gomod2nix.packages.${system}.default
            pkgs.just
            bob.packages.${system}.batman
            bob.packages.${system}.tap-dancer
          ];
        };
      }
    );
}
