package proc

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/stubbedev/lys-polkit/internal/polkit"
)

type FS string

const System FS = "/proc"

func (root FS) Name(pid polkit.PID) (string, error) {
	dir := filepath.Join(string(root), strconv.FormatUint(uint64(pid), 10))
	if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
		return filepath.Base(exe), nil
	}
	comm, err := os.ReadFile(filepath.Join(dir, "comm")) //nolint:gosec // a path under the proc root, built from a process id
	if err != nil {
		return "", fmt.Errorf("name of process %d: %w", pid, err)
	}
	return string(bytes.TrimSuffix(comm, []byte("\n"))), nil
}
