<p align="center">
  <img src="docs/public/lys-polkit.svg" width="160" alt="lys-polkit">
</p>

<h1 align="center">lys-polkit</h1>

<p align="center">
  The lys desktop's polkit authentication agent, in place of hyprpolkitagent.
</p>

<p align="center">
  <a href="https://github.com/stubbedev/lys-polkit/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/stubbedev/lys-polkit/ci.yml?branch=main&style=for-the-badge&label=CI" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue?style=for-the-badge" alt="MIT license"></a>
  <a href="https://stubbedev.github.io/lys-polkit/"><img src="https://img.shields.io/badge/docs-stubbedev.github.io-informational?style=for-the-badge" alt="Documentation"></a>
</p>

When a program asks polkitd for an action that needs authentication (`pkexec`,
a settings panel, a package manager), polkitd asks the agent registered for the
user's session. lys-polkit runs the PAM conversation through polkit's agent
helper and asks for the password, or shows the fingerprint prompt, in
[lys-shell](https://github.com/stubbedev/lys-shell)'s authentication dialog.

## Install

With nix and home-manager:

```nix
{
  inputs.lys-polkit.url = "github:stubbedev/lys-polkit";

  # in the home-manager configuration
  imports = [inputs.lys-polkit.homeManagerModules.default];
  services.lys-polkit.enable = true;
  # one agent per session: drop the one it replaces
  services.hyprpolkitagent.enable = false;
}
```

To build it by hand on another distribution, see
[Install](https://stubbedev.github.io/lys-polkit/guide/install).

## Usage

lys-polkit runs as a systemd user service with the graphical session, and
needs polkitd, systemd-logind and lys-shell. There is nothing to call. To check
it, ask for an authentication and answer in the dialog:

```sh
systemctl --user status lys-polkit
pkexec --disable-internal-agent true
```

## Documentation

The [site](https://stubbedev.github.io/lys-polkit/) covers
[getting started](https://stubbedev.github.io/lys-polkit/guide/getting-started),
[install](https://stubbedev.github.io/lys-polkit/guide/install),
[configuration](https://stubbedev.github.io/lys-polkit/guide/configuration),
[how it works](https://stubbedev.github.io/lys-polkit/guide/how-it-works) and
[development](https://stubbedev.github.io/lys-polkit/guide/development).

## The lys family

lys-polkit is one of the projects that make the lys desktop:
[lys-shell](https://github.com/stubbedev/lys-shell),
[lys-wm](https://github.com/stubbedev/lys-wm),
[lys-ui](https://github.com/stubbedev/lys-ui),
[lys-lib](https://github.com/stubbedev/lys-lib),
[lys-login](https://github.com/stubbedev/lys-login),
[lys-splash](https://github.com/stubbedev/lys-splash) and
[lys-secrets](https://github.com/stubbedev/lys-secrets).
