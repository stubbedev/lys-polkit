{
  pkgs,
  lys-polkit,
  fakedialogs,
}: let
  directoryUID = 1500;
  keepAction = "dev.stubbe.lys.test.keep";
  keepSeconds = 20;
  keepPolicy = pkgs.writeTextDir "share/polkit-1/actions/${keepAction}.policy" ''
    <?xml version="1.0" encoding="UTF-8"?>
    <!DOCTYPE policyconfig PUBLIC "-//freedesktop//DTD PolicyKit Policy Configuration 1.0//EN" "http://www.freedesktop.org/standards/PolicyKit/1/policyconfig.dtd">
    <policyconfig>
      <vendor>lys test</vendor>
      <action id="${keepAction}">
        <description>Keep an admin authorization</description>
        <message>Authentication is required to test retained authorizations</message>
        <defaults>
          <allow_any>auth_admin_keep</allow_any>
          <allow_inactive>auth_admin_keep</allow_inactive>
          <allow_active>auth_admin_keep</allow_active>
        </defaults>
      </action>
    </policyconfig>
  '';
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
      settings.Polkitd.ExpirationSeconds = keepSeconds;
    };
    environment.systemPackages = [keepPolicy];
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

    retained_cmd = "busctl --user --machine=alice@ get-property dev.stubbe.lys.Polkit1 /dev/stubbe/lys/Polkit dev.stubbe.lys.Polkit1 TemporaryAuthorizations"

    def retained():
        return machine.succeed(retained_cmd).strip()

    def asked():
        return int(machine.succeed("grep -c '^authenticate ' /tmp/vm/log").strip())

    def pkcheck(pid):
        return machine.execute(f"systemd-run --machine=alice@ --user --pipe --wait --quiet ${pkgs.lib.getBin pkgs.polkit}/bin/pkcheck --action-id ${keepAction} --process {pid} --allow-user-interaction </dev/null 2>&1")

    with subtest("an auth_admin_keep authorization is listed, reused, revoked and expires"):
        machine.succeed("systemd-run --machine=alice@ --user --unit=holder ${pkgs.lib.getExe' pkgs.coreutils "sleep"} infinity")
        holder = machine.succeed("systemctl --user --machine=alice@ show --property=MainPID --value holder").strip()
        assert retained() == "a(sstt) 0", retained()
        script("answer hunter2")
        before = asked()
        status, out = pkcheck(holder)
        assert status == 0 and asked() == before + 1, f"pkcheck exited {status} after {asked() - before} prompts: {out}"
        machine.wait_until_succeeds(retained_cmd + " | grep -q '^a(sstt) 1 '", timeout=10)
        fields = retained().split()
        assert fields[3] == '"${keepAction}"', retained()
        assert abs(int(fields[5]) - int(fields[4]) - ${toString keepSeconds}) <= 1, retained()

        status, out = pkcheck(holder)
        assert status == 0 and asked() == before + 1, f"the retained authorization was not reused: pkcheck exited {status}, {asked() - before} prompts: {out}"

        machine.succeed("systemd-run --machine=alice@ --user --pipe --wait --quiet ${pkgs.lib.getExe lys-polkit} revoke")
        machine.wait_until_succeeds(retained_cmd + " | grep -qx 'a(sstt) 0'", timeout=10)
        status, out = pkcheck(holder)
        assert status == 0 and asked() == before + 2, f"after revoke the action did not ask again: pkcheck exited {status}, {asked() - before} prompts: {out}"

        machine.wait_until_succeeds(retained_cmd + " | grep -q '^a(sstt) 1 '", timeout=10)
        machine.wait_until_succeeds(retained_cmd + " | grep -qx 'a(sstt) 0'", timeout=${toString (keepSeconds + 15)})
        machine.succeed("systemctl --user --machine=alice@ stop holder")

    with subtest("with lys-polkit stopped pkexec finds no agent"):
        machine.succeed("systemctl --user --machine=alice@ stop lys-polkit.service")
        machine.wait_until_succeeds("journalctl -u polkit.service | grep -q 'Unregistered Authentication Agent'")
        status, out = pkexec()
        assert status != 0 and "No authentication agent found" in out, f"pkexec exited {status}: {out}"
  '';
}
