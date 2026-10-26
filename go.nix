# go.nix — this module's dependencies (FDR 0008); go.mod, gomod2nix.toml and
# the package graph are rendered or derived from it inside nix. Edit through
# the escape hatch (godyn-go) or by hand.
{
  flakeInputs = { };
  go = "1.26.0";
  module = "code.linenisgreat.com/tommy";
  replace = { };
  require = {
    "github.com/dave/jennifer" = {
      go = "1.20";
      hash = "sha256-tKr4x0m+Nup2X9UyxIK+5ZXzrb1vyfPzc9hD2xh6H6g=";
      version = "v1.7.1";
    };
    "golang.org/x/mod" = {
      go = "1.26.0";
      hash = "sha256-jgjKlIzteE99u8/TbuZTk4cLAwDOVZePXaRwqEpYvPk=";
      indirect = true;
      version = "v0.42.0";
    };
    "golang.org/x/sync" = {
      go = "1.26.0";
      hash = "sha256-gViyLpsZFBKTEUtK6Li3m6plz5J/VCAcpTLymQVRpj4=";
      indirect = true;
      version = "v0.24.0";
    };
    "golang.org/x/tools" = {
      go = "1.26.0";
      hash = "sha256-keFn6eH6Oa54T4r9msvMw1q4wqJ2yRadGTb/TbPvmXI=";
      version = "v0.52.0";
    };
    "mvdan.cc/gofumpt" = {
      go = "1.25.0";
      hash = "sha256-HPMtCqdfgOupeuLPgiU7UBszA3MQOP/W2KSwOSGFzs4=";
      version = "v0.10.0";
    };
  };
}
