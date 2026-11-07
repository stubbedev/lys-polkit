package proc_test

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-polkit/internal/polkit"
	"github.com/stubbedev/lys-polkit/internal/proc"
)

func TestNameOfThisProcess(t *testing.T) {
	t.Parallel()

	got, err := proc.System.Name(polkit.PID(os.Getpid()))
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	if want := filepath.Base(exe); got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}
}

func TestNameOfAGoneProcess(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	if name, err := proc.System.Name(polkit.PID(cmd.Process.Pid)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Name of reaped pid %d = %q, %v; want ErrNotExist", cmd.Process.Pid, name, err)
	}
}

func TestNameFallsBackToComm(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "4242")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "comm"), []byte("pkexec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := proc.FS(root).Name(4242); err != nil || got != "pkexec" {
		t.Errorf("Name = %q, %v; want pkexec from comm when exe cannot be read", got, err)
	}
}
