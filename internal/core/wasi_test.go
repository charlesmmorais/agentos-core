package core

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestWASIMissionRecovery(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("durable host is Linux")
	}
	root := t.TempDir()
	helper := filepath.Join(root, "agentos-wasi")
	module := filepath.Join(root, "task.wasm")
	for _, build := range []struct {
		path, pkg string
		wasm      bool
	}{{helper, "../../cmd/agentos-wasi", false}, {module, "../wasiruntime/testdata/task", true}} {
		cmd := exec.Command("go", "build", "-o", build.path, build.pkg)
		if build.wasm {
			cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %s %v", out, err)
		}
	}
	p := Protocol{Mission: "delay", Workspace: root, Script: module, IntervalSeconds: 1, TimeoutSeconds: 5, MaxCycles: 3}
	st, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	st.Execution, err = InspectWASI(p, helper)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the workspace separate from executables and backups.
	st.Protocol.Workspace = t.TempDir()
	stateDir := filepath.Join(root, "state")
	s, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	if err = Tick(ctx, s, st, SelectExecutor(st.Execution), time.Now()); err == nil {
		t.Fatal("interrupted cycle succeeded")
	}
	cancel()
	pending := st.Pending
	if pending == "" || st.Completed != 0 {
		t.Fatal("lost pending reservation")
	}
	s.Close()
	s, err = Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	st, err = s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err = Tick(context.Background(), s, st, SelectExecutor(st.Execution), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	json.Unmarshal(st.Memory, &result)
	if result["cycle_id"] != pending || st.Completed != 1 || len(st.Artifacts) != 1 || st.Pending != "" {
		t.Fatal("replay changed identity or count", st, result)
	}
	// Backup includes a multi-MiB module, but never includes the helper executable.
	archive := filepath.Join(root, "backup.tar")
	report, err := s.Backup(archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyBackup(archive, report.SHA256); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "restored")
	if _, err = RestoreBackup(archive, report.SHA256, dest); err != nil {
		t.Fatal(err)
	}
	recovered, err := Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	restored, err := recovered.Load()
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != "paused" || restored.Protocol.Script != filepath.Join(dest, "module.wasm") || restored.AgentID != st.AgentID {
		t.Fatal("invalid restored WASI mission")
	}
	c := NewController(recovered, restored, SelectExecutor(restored.Execution))
	if c.Control("resume") == nil {
		t.Fatal("bypassed recovery gate")
	}
	if err = ActivateRecovery(recovered, restored, true); err != nil {
		t.Fatal(err)
	}
	if err = c.Control("resume"); err != nil {
		t.Fatal(err)
	}
	if err = c.Step(context.Background(), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().Completed != 2 {
		t.Fatal("restored mission did not continue")
	}
	// Verify before launching even a syntactically valid modified module.
	b, _ := os.ReadFile(module)
	b[len(b)-1] ^= 1
	os.WriteFile(module, b, 0600)
	if _, err = (WASI{st.Execution}).Execute(context.Background(), st.Protocol, Request{}); err == nil {
		t.Fatal("module tamper accepted")
	}
	if _, err = s.Backup(filepath.Join(root, "tampered.tar")); err == nil {
		t.Fatal("tampered module backed up")
	}
	helperBytes, _ := os.ReadFile(helper)
	helperBytes[len(helperBytes)-1] ^= 1
	os.WriteFile(helper, helperBytes, 0700)
	if _, err = (WASI{restored.Execution}).Execute(context.Background(), restored.Protocol, Request{}); err == nil {
		t.Fatal("helper tamper accepted")
	}
}
