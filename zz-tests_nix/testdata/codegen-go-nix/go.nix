# The codegen-go-nix fixture's only module description (igloo FDR 0008): its
# go.mod is rendered from this file inside nix, never tracked.
{
  module = "example.com/tommy-codegen-go-nix";
  go = "1.26";
  flakeInputs."code.linenisgreat.com/tommy".input = "tommy";
}
