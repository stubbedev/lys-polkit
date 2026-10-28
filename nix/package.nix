{self, ...}: {
  perSystem = {
    lib,
    pkgs,
    ...
  }: {
    packages.default = pkgs.buildGo127Module {
      pname = "lys-polkit";
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

      vendorHash = "sha256-Dqsq1pS1CCK3LCtTmMkI/Lr3ZxyRJUlgvu5oyw81uiw=";

      nativeCheckInputs = [pkgs.dbus];

      env.CGO_ENABLED = 0;

      doCheck = true;

      meta = {
        description = "The lys desktop's polkit authentication agent";
        homepage = "https://github.com/stubbedev/lys-polkit";
        license = lib.licenses.mit;
        mainProgram = "lys-polkit";
        platforms = lib.platforms.unix;
      };
    };
  };
}
