{
  lib,
  buildGo127Module,
  dbus,
  getent,
  version,
  src,
}:
buildGo127Module {
  pname = "lys-polkit";
  inherit version src;

  vendorHash = "sha256-5n8NJImdemwkZPJgp7Tu1FN0sHMLyUYrHGkwR2RIfmA=";

  subPackages = ["."];

  env.CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-X main.version=${version}"
  ];

  nativeCheckInputs = [dbus getent];
  doCheck = true;

  postInstall = ''
    install -Dm644 dist/lys-polkit.service.in $out/lib/systemd/user/lys-polkit.service
    substituteInPlace $out/lib/systemd/user/lys-polkit.service --replace-fail @bindir@ $out/bin
  '';

  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    $out/bin/lys-polkit --version | grep -qxF "lys-polkit ${version}"
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
