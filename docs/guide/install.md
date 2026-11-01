# Install

Whichever way you install it, keep one agent per session: disable the polkit
agent the session started before.

## With home-manager

The flake exports a home-manager module, for NixOS or any other distribution
with nix:

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

It installs the package and the `lys-polkit.service` user unit, wanted by
`graphical-session.target`.

On NixOS, `security.polkit.enable = true` provides polkitd and polkit 127's
helper socket, which lys-polkit uses. On another distribution polkit 127's
socket is used too. An older polkit needs its
[helper path](./configuration#build-time-values) set.

## Without nix

You need the Go version `go.mod` declares. Build with your distribution's
helper path (Debian and Ubuntu shown), install the binary and the user unit,
and enable it for the graphical session:

```sh
git clone https://github.com/stubbedev/lys-polkit
cd lys-polkit
go build -ldflags "-X main.version=$(git describe --always) -X main.helperProgram=/usr/lib/polkit-1/polkit-agent-helper-1" -o lys-polkit .
install -Dm755 lys-polkit ~/.local/bin/lys-polkit
mkdir -p ~/.config/systemd/user
sed "s|@bindir@|$HOME/.local/bin|" dist/lys-polkit.service.in > ~/.config/systemd/user/lys-polkit.service
systemctl --user daemon-reload
systemctl --user enable --now lys-polkit.service
```

Then [check that it works](./getting-started#check-that-it-works).
