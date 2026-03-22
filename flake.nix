{
  description = "Tommy: a TOML library for Go";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/3e20095fe3c6cbb1ddcef89b26969a69a1570776";
    nixpkgs-master.url = "github:NixOS/nixpkgs/ca82feec736331f4c438121a994344e08ed547f5";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
    go = {
      url = "github:amarbel-llc/purse-first?dir=devenvs/go";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
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
      go,
      bob,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            go.overlays.default
          ];
        };
      in
      {
        packages = {
          default = pkgs.buildGoModule {
            pname = "tommy";
            version = "0.1.0";
            src = ./.;
            subPackages = [ "cmd/tommy" ];
            vendorHash = null;

            meta = {
              description = "A TOML library for Go";
              homepage = "https://github.com/amarbel-llc/tommy";
              license = pkgs.lib.licenses.mit;
            };
          };
        };

        devShells.default = pkgs.mkShell {
          inputsFrom = [
            go.devShells.${system}.default
          ];
          packages = [
            bob.packages.${system}.batman
            bob.packages.${system}.tap-dancer
          ];
        };
      }
    );
}
