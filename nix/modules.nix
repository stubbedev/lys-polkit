{self, ...}: {
  flake.homeManagerModules.default = import ./hm-module.nix self;
}
