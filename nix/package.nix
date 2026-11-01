{
  perSystem = {
    lib,
    pkgs,
    ...
  }: let
    src = lib.fileset.toSource {
      root = ../.;
      fileset = lib.fileset.difference (lib.fileset.gitTracked ../.) (
        lib.fileset.unions [
          ../flake.nix
          ../flake.lock
          ../nix
          ../docs
        ]
      );
    };
  in {
    packages.default = pkgs.callPackage ./lys-polkit.nix {
      version = "0-unstable-${lib.substring 0 8 (builtins.unsafeDiscardStringContext (baseNameOf src))}";
      inherit src;
    };
  };
}
