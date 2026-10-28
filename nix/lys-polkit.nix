{
  lib,
  buildGo127Module,
  dbus,
  version,
  src,
  helperProgram ? "/run/wrappers/bin/polkit-agent-helper-1",
}:
buildGo127Module {
  pname = "lys-polkit";
  inherit version src;

  vendorHash = "sha256-DXfR61OtJ0mXcRSeRY0IRGwraZ28WCDaK7nLGdMUvhE=";

  subPackages = ["."];

  env.CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-X main.version=${version}"
    "-X main.helperProgram=${helperProgram}"
  ];

  nativeCheckInputs = [dbus];
  doCheck = true;

  postInstall = ''
    install -Dm644 dist/lys-polkit.service.in $out/lib/systemd/user/lys-polkit.service
    substituteInPlace $out/lib/systemd/user/lys-polkit.service --replace-fail @bindir@ $out/bin
  '';

  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    $out/bin/lys-polkit --version | grep -qxF "lys-polkit ${version}"
    strings $out/bin/lys-polkit | grep -qF ${helperProgram}
    grep -qxF "ExecStart=$out/bin/lys-polkit" $out/lib/systemd/user/lys-polkit.service
    runHook postInstallCheck
  '';

  meta = {
    description = "The lys desktop's polkit authentication agent";
    homepage = "https://github.com/stubbedev/lys-polkit";
    license = lib.licenses.mit;
    mainProgram = "lys-polkit";
    platforms = lib.platforms.linux;
  };
}
