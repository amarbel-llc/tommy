# go.nix — this module's dependencies (FDR 0008); go.mod, gomod2nix.toml and
# the package graph are rendered or derived from it inside nix. Edit through
# the escape hatch (godyn-go) or by hand.
{
  flakeInputs = { };
  go = "1.26";
  module = "code.linenisgreat.com/tommy";
  replace = { };
  require = {
    "github.com/dave/jennifer" = {
      go = "1.20";
      hash = "sha256-tKr4x0m+Nup2X9UyxIK+5ZXzrb1vyfPzc9hD2xh6H6g=";
      version = "v1.7.1";
    };
    "golang.org/x/mod" = {
      go = "1.25.0";
      hash = "sha256-ICEQxokHywOFInDPqoP+go9l1tZSz3roknF5SXPtNV4=";
      indirect = true;
      version = "v0.35.0";
    };
    "golang.org/x/sync" = {
      go = "1.25.0";
      hash = "sha256-ybcjhCfK6lroUM0yswUvWooW8MOQZBXyiSqoxG6Uy0Y=";
      indirect = true;
      version = "v0.20.0";
    };
    "golang.org/x/tools" = {
      go = "1.25.0";
      hash = "sha256-xuj5FLtSJsAojLLTLXtPdLAIFNTKoVFbDMuqRXmj2W4=";
      version = "v0.44.0";
    };
    "mvdan.cc/gofumpt" = {
      go = "1.25.0";
      hash = "sha256-HPMtCqdfgOupeuLPgiU7UBszA3MQOP/W2KSwOSGFzs4=";
      version = "v0.10.0";
    };
  };
}
