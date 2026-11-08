<p align="center">
  <img src="docs/public/lys-polkit.svg" width="160" alt="lys-polkit">
</p>

<h1 align="center">lys-polkit</h1>

<p align="center">
  The lys desktop's polkit authentication agent.
</p>

<p align="center">
  <a href="https://github.com/stubbedev/lys-polkit/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/stubbedev/lys-polkit/ci.yml?branch=main&style=for-the-badge&label=CI" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue?style=for-the-badge" alt="MIT license"></a>
  <a href="https://stubbedev.github.io/lys-polkit/"><img src="https://img.shields.io/badge/docs-stubbedev.github.io-informational?style=for-the-badge" alt="Documentation"></a>
</p>

lys-polkit registers with polkitd as the session's authentication agent. When a
program requests an action that needs authentication (`pkexec`, a settings
panel, a package manager), lys-polkit runs the PAM conversation through
polkit's agent helper and shows it in
[lys-shell](https://github.com/stubbedev/lys-shell)'s authentication dialog.

## Install

With nix and home-manager:

```nix
{
  inputs.lys-polkit.url = "github:stubbedev/lys-polkit";

  # in the home-manager configuration
  imports = [inputs.lys-polkit.homeManagerModules.default];
  services.lys-polkit.enable = true;
  services.hyprpolkitagent.enable = false; # one agent per session
}
```

Without nix, see [Install](https://stubbedev.github.io/lys-polkit/guide/install).

## Usage

It runs as a systemd user service with the graphical session and needs
polkitd, systemd-logind and lys-shell.

```sh
systemctl --user status lys-polkit
pkexec --disable-internal-agent true   # opens the dialog
lys-polkit revoke                      # drop retained admin authorizations, like sudo -k
```

## Documentation

[stubbedev.github.io/lys-polkit](https://stubbedev.github.io/lys-polkit/)

## The lys family

[lys-shell](https://github.com/stubbedev/lys-shell),
[lys-wm](https://github.com/stubbedev/lys-wm),
[lys-ui](https://github.com/stubbedev/lys-ui),
[lys-lib](https://github.com/stubbedev/lys-lib),
[lys-login](https://github.com/stubbedev/lys-login),
[lys-splash](https://github.com/stubbedev/lys-splash),
[lys-secrets](https://github.com/stubbedev/lys-secrets).
