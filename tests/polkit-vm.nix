{
  pkgs,
  lys-polkit,
  fakedialogs,
}:
pkgs.testers.runNixOSTest {
  name = "lys-polkit";

  nodes.machine = {
    security.polkit = {
      enable = true;
      enablePkexecWrapper = true;
      extraArgs = ["--no-debug" "--log-level=info"];
    };
    users.users.alice = {
      isNormalUser = true;
      password = "hunter2";
      extraGroups = ["wheel"];
    };
    services.getty.autologinUser = "alice";
    systemd.packages = [lys-polkit];
    systemd.user.services.fakedialogs.serviceConfig.ExecStart = "${pkgs.lib.getExe fakedialogs} -script /tmp/vm/script -log /tmp/vm/log";
    systemd.tmpfiles.rules = ["d /tmp/vm 0777 root root -"];
  };

  testScript = ''
    def pkexec():
        return machine.execute("systemd-run --machine=alice@ --user --pipe --wait --quiet /run/wrappers/bin/pkexec --disable-internal-agent true </dev/null 2>&1")

    def script(*lines):
        machine.succeed("printf '%s\\n' " + " ".join(f"'{l}'" for l in lines) + " > /tmp/vm/script")

    machine.wait_for_unit("multi-user.target")
    machine.wait_until_succeeds("test -n \"$(loginctl show-user alice --property=Display --value)\"")
    machine.succeed("systemctl --user --machine=alice@ start fakedialogs.service lys-polkit.service")
    machine.wait_until_succeeds("grep -q ready /tmp/vm/log")
    machine.wait_until_succeeds("journalctl -u polkit.service | grep -q 'Registered Authentication Agent'")

    with subtest("the right password authorizes"):
        script("answer hunter2")
        status, out = pkexec()
        assert status == 0, f"pkexec exited {status}: {out}"

    with subtest("a wrong password, then the right one, authorizes"):
        script("answer wrong", "answer hunter2")
        status, out = pkexec()
        assert status == 0, f"pkexec exited {status}: {out}"
        machine.succeed("grep -q 'notes=\\[error:\"Authentication failed\"\\]' /tmp/vm/log")

    with subtest("cancelling makes pkexec exit 126"):
        script("cancel")
        status, out = pkexec()
        assert status == 126, f"pkexec exited {status}: {out}"

    with subtest("with lys-polkit stopped pkexec finds no agent"):
        machine.succeed("systemctl --user --machine=alice@ stop lys-polkit.service")
        machine.wait_until_succeeds("journalctl -u polkit.service | grep -q 'Unregistered Authentication Agent'")
        status, out = pkexec()
        assert status != 0 and "No authentication agent found" in out, f"pkexec exited {status}: {out}"
  '';
}
