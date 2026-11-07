{
  pkgs,
  lys-polkit,
  fakedialogs,
}: let
  directoryUID = 1500;
  directoryUser = builtins.toJSON {
    userName = "carol";
    uid = directoryUID;
    gid = directoryUID;
    disposition = "regular";
  };
in
pkgs.testers.runNixOSTest {
  name = "lys-polkit";

  nodes.machine = {
    security.polkit = {
      enable = true;
      enablePkexecWrapper = true;
      extraArgs = ["--no-debug" "--log-level=info"];
      adminIdentities = ["unix-user:carol" "unix-group:wheel"];
    };
    security.wrappers.polkit-agent-helper-1 = {
      setuid = true;
      owner = "root";
      group = "root";
      source = "${pkgs.lib.getLib pkgs.polkit}/lib/polkit-1/polkit-agent-helper-1";
    };
    users.users.alice = {
      isNormalUser = true;
      password = "hunter2";
      extraGroups = ["wheel"];
    };
    environment.etc."userdb/carol.user".text = directoryUser;
    environment.etc."userdb/${toString directoryUID}.user".text = directoryUser;
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

    with subtest("the details name the action, its vendor and the calling program"):
        machine.succeed("grep -q 'details=\\[Action:\"org.freedesktop.policykit.exec\" Vendor:\"The polkit project\" ' /tmp/vm/log")
        machine.succeed("grep -q 'Caller:\"pkexec ([0-9]*)\"\\]' /tmp/vm/log")

    with subtest("a user only the name service knows is offered by name"):
        machine.fail("grep -q '^carol:' /etc/passwd")
        machine.succeed("getent passwd ${toString directoryUID} | grep -q '^carol:'")
        machine.succeed("grep -q 'identities=[^ ]*carol' /tmp/vm/log")

    with subtest("a wrong password, then the right one, authorizes"):
        script("answer wrong", "answer hunter2")
        status, out = pkexec()
        assert status == 0, f"pkexec exited {status}: {out}"
        machine.succeed("grep -q 'notes=\\[error:\"Authentication failed\"\\]' /tmp/vm/log")

    with subtest("cancelling makes pkexec exit 126"):
        script("cancel")
        status, out = pkexec()
        assert status == 126, f"pkexec exited {status}: {out}"

    with subtest("without the helper socket the setuid helper authorizes"):
        machine.succeed("systemctl stop polkit-agent-helper.socket && ! test -e /run/polkit/agent-helper.socket")
        script("answer hunter2")
        status, out = pkexec()
        machine.succeed("systemctl start polkit-agent-helper.socket")
        assert status == 0, f"pkexec exited {status}: {out}"

    with subtest("with lys-polkit stopped pkexec finds no agent"):
        machine.succeed("systemctl --user --machine=alice@ stop lys-polkit.service")
        machine.wait_until_succeeds("journalctl -u polkit.service | grep -q 'Unregistered Authentication Agent'")
        status, out = pkexec()
        assert status != 0 and "No authentication agent found" in out, f"pkexec exited {status}: {out}"
  '';
}
