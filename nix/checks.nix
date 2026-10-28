{
  self,
  inputs,
  ...
}: {
  perSystem = {
    lib,
    pkgs,
    ...
  }: let
    pkg = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
    home = inputs.home-manager.lib.homeManagerConfiguration {
      inherit pkgs;
      modules = [
        self.homeManagerModules.default
        {
          home = {
            username = "alice";
            homeDirectory = "/home/alice";
            stateVersion = "25.11";
          };
          services.lys-polkit.enable = true;
        }
      ];
    };
    files = home.config.xdg.configFile;
    unit = files."systemd/user/lys-polkit.service".source;
    wants = files."systemd/user/graphical-session.target.wants/lys-polkit.service".source;
  in {
    checks =
      lib.optionalAttrs pkgs.stdenv.hostPlatform.isx86_64 {
        lys-polkit-vm = pkgs.callPackage ../tests/polkit-vm.nix {
          lys-polkit = pkg;
          fakedialogs = pkg.overrideAttrs (old: {
            pname = "lys-polkit-fakedialogs";
            subPackages = ["internal/vmtest/fakedialogs"];
            ldflags = [];
            doCheck = false;
            postInstall = "";
            doInstallCheck = false;
            meta = old.meta // {mainProgram = "fakedialogs";};
          });
        };
      }
      // lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
        lys-polkit-hm-module = pkgs.runCommand "lys-polkit-hm-module" {} ''
          grep -qxF "PartOf=graphical-session.target" ${unit}
          grep -qxF "After=graphical-session.target" ${unit}
          grep -qxF "WantedBy=graphical-session.target" ${unit}
          grep -qxF "Restart=on-failure" ${unit}
          grep -qxF "ExecStart=${lib.getExe pkg}" ${unit}
          [ "${wants}" = "${unit}" ]
          ${lib.optionalString (!lib.elem pkg home.config.home.packages) "echo 'lys-polkit is not in home.packages' >&2; exit 1"}
          touch $out
        '';
      };
  };
}
