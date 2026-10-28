{self, ...}: {
  perSystem = {
    lib,
    pkgs,
    ...
  }: {
    packages.default = pkgs.callPackage ./lys-polkit.nix {
      version = "0-unstable-${self.lastModifiedDate or "0"}";
      src = lib.fileset.toSource {
        root = ../.;
        fileset = lib.fileset.difference (lib.fileset.gitTracked ../.) (
          lib.fileset.unions [
            ../flake.nix
            ../flake.lock
            ../nix
          ]
        );
      };
    };
  };
}
