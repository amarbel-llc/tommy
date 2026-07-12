# tommy's conformist overlay, merged with conformist.lib.presets.{eng,eng-go}
# in flake.nix. The eng preset enables the eng-convention linters
# (eng-versioning, flake-outputs/lock, the justfile-* roster); eng-go carries
# the canonical goimports -> gofumpt chain. Here live the repo-specific
# formatters, the shellcheck linter, and excludes.
{ pkgs, ... }:
{
  programs.nixfmt.enable = true;

  # shfmt: a raw stanza rather than `programs.shfmt.enable`. The module cannot
  # emit `-ci` (no option for it) and its default includes lack `*.bats` — both
  # required by the eng shell style: 2-space indent, simplify, case-branch
  # indent; over *.sh / *.bash / *.bats. (Same rationale as maneater's overlay.)
  settings.formatter.shfmt = {
    command = "${pkgs.shfmt}/bin/shfmt";
    options = [
      "-w"
      "-i"
      "2"
      "-s"
      "-ci"
    ];
    includes = [
      "*.sh"
      "*.bash"
      "*.bats"
    ];
  };

  # shellcheck linter (read-only in `conformist check`). The module's default
  # includes lack *.bats, which the zz-tests_bats suite uses.
  linters.shellcheck.enable = true;
  linters.shellcheck.includes = [
    "*.sh"
    "*.bash"
    "*.bats"
  ];

  # eng-versioning(7): go.mod's module path derives the key as TOMMY_VERSION;
  # pinned explicitly to document the version.env contract.
  linters.eng-versioning.key = "TOMMY_VERSION";

  # tommy IS the TOML formatter (tommy fmt), so we do NOT wire tommy-fmt for
  # *.toml here: consuming this repo's own binary in its own formatting check
  # would create a bootstrap cycle (tommy can't be built before it can format
  # its own source). Downstream consumers import tommy.conformistModule.${system}
  # from this flake to get the tommy-backed formatter without the cycle.
  # See the exported `conformistModule` in flake.nix.

  # Excludes layered on conformist's default-excludes (*.lock, go.mod, go.sum,
  # LICENSE). Only genuine scratch artifacts: *.md has no enabled formatter here;
  # .tmp/ holds session-local scratch files; result/result-* are nix out-links.
  settings.excludes = [
    "*.md"
    ".tmp/**"
    "result"
    "result-*"
  ];
}
