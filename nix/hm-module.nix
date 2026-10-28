self: {
  config,
  lib,
  pkgs,
  ...
}: let
  cfg = config.services.lys-polkit;
  unit = "lys-polkit.service";
in {
  options.services.lys-polkit = {
    enable = lib.mkEnableOption "lys-polkit, the lys desktop's polkit authentication agent, in the graphical session";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "lys-polkit.packages.\${system}.default";
      description = "The lys-polkit package, whose user unit is installed.";
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [cfg.package];

    xdg.configFile = {
      "systemd/user/${unit}".source = "${cfg.package}/share/systemd/user/${unit}";
      "systemd/user/graphical-session.target.wants/${unit}".source = "${cfg.package}/share/systemd/user/${unit}";
    };
  };
}
