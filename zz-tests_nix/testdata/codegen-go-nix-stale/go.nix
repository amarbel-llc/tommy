# The codegen-go-nix fixture's module (see ../codegen-go-nix/go.nix) with a
# committed config_tommy.go whose header names a tommy rev no build carries
# (0000000): the stale companion a tommy bump leaves behind (tommy#143). Only its
# header is asserted on, so a change in tommy's generated body needs no update.
{
  module = "example.com/tommy-codegen-go-nix";
  go = "1.26";
  flakeInputs."code.linenisgreat.com/tommy".input = "tommy";
}
