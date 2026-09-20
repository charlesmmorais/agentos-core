package core

import (
	"github.com/charlesmmorais/agentos-core/internal/kernel"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnsupportedHostNoMutation(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("unsupported host test")
	}
	dest := filepath.Join(t.TempDir(), "state")
	if _, err := Open(dest); err == nil {
		t.Fatal("unsupported store accepted")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("state directory created")
	}
	if _, err := RestoreBackup("missing", "", dest); err == nil {
		t.Fatal("unsupported restore accepted")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("restore directory created")
	}
}
func TestHostRejectsForeignPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux host")
	}
	p := kernel.Protocol{Mission: "test", Workspace: `C:\data`, Script: `C:\script.py`, IntervalSeconds: 1, TimeoutSeconds: 1, MaxCycles: 1}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := New(p); err == nil {
		t.Fatal("host accepted foreign paths")
	}
}
