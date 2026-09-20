package core

import (
	"archive/tar"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func backupFixture(t *testing.T) (*Store, *State) {
	t.Helper()
	s, st := setup(t)
	st.Protocol.Script = filepath.Join(t.TempDir(), "observe.py")
	source := []byte("print('{}')\n")
	if err := os.WriteFile(st.Protocol.Script, source, 0600); err != nil {
		t.Fatal(err)
	}
	st.Execution = &Manifest{ScriptSHA256: digest(source)}
	os.WriteFile(filepath.Join(st.Protocol.Workspace, "data.txt"), []byte("source evidence"), 0600)
	if err := Tick(context.Background(), s, st, &fake{}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return s, st
}
func TestBackupRestorePreservesIdentityAndBlocksActivation(t *testing.T) {
	s, st := backupFixture(t)
	st.ModelAttempts = 2
	st.Pending = st.AgentID + ":2"
	st.Writes = &WritePolicy{"http://127.0.0.1:1", 3}
	c := NewController(s, st, &fake{})
	a, err := c.ActionControl("propose", "", "", RecordPayload{"record", "payload"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.ActionControl("approve", a.ID, a.Digest, RecordPayload{}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "backup.tar")
	report, err := s.Backup(archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Backup(archive); err == nil {
		t.Fatal("overwrote backup")
	}
	if _, err = VerifyBackup(archive, report.SHA256); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored")
	if _, err = RestoreBackup(archive, report.SHA256, dest); err != nil {
		t.Fatal(err)
	}
	restoredStore, err := Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredStore.Close()
	restored, err := restoredStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if restored.AgentID != st.AgentID || restored.Completed != 1 || restored.ModelAttempts != 2 || restored.Pending != st.Pending || restored.Status != "paused" || restored.Recovery.Ready {
		t.Fatal("recovery lost identity/budget or activated automatically")
	}
	if restored.Actions[0].Status != "unknown" || restored.Actions[0].Digest != a.Digest || restored.Actions[0].ApprovedDigest != a.Digest {
		t.Fatal("stale approval not fenced")
	}
	if restored.Protocol.Script != filepath.Join(dest, "script.py") || restored.Protocol.Workspace != filepath.Join(dest, "workspace") {
		t.Fatal("paths not relocated")
	}
	controller := NewController(restoredStore, restored, &fake{})
	if controller.Control("resume") == nil || controller.RunAction(context.Background(), a.ID, false) == nil {
		t.Fatal("recovery gate bypassed")
	}
	if ActivateRecovery(restoredStore, restored, false) == nil {
		t.Fatal("source fencing acknowledgement missing")
	}
	if _, err = RestoreBackup(archive, report.SHA256, dest); err == nil {
		t.Fatal("overwrote restored state")
	}
	if _, err = restoredStore.ReadArtifact(restored.Artifacts[0]); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRefusesCorruptArtifact(t *testing.T) {
	s, st := backupFixture(t)
	os.WriteFile(filepath.Join(s.Dir, "artifacts", st.Artifacts[0].SHA256+".json"), []byte("corrupt"), 0600)
	path := filepath.Join(t.TempDir(), "backup.tar")
	if _, err := s.Backup(path); err == nil {
		t.Fatal("corrupt artifact backed up")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("partial backup published")
	}
}

func TestBackupRejectsUnsafeArchives(t *testing.T) {
	for _, mode := range []string{"traversal", "absolute", "symlink", "duplicate", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "unsafe.tar")
			f, _ := os.Create(file)
			w := tar.NewWriter(f)
			h := &tar.Header{Name: "state.json", Mode: 0600, Typeflag: tar.TypeReg, Size: 2}
			switch mode {
			case "traversal":
				h.Name = "workspace/../escape"
			case "absolute":
				h.Name = "/tmp/escape"
			case "symlink":
				h.Typeflag = tar.TypeSymlink
				h.Linkname = "/etc/passwd"
				h.Size = 0
			case "oversize":
				h.Name = "workspace/large"
				h.Size = 4*1024*1024 + 1
			}
			w.WriteHeader(h)
			if h.Size == 2 {
				w.Write([]byte("{}"))
			}
			if mode == "duplicate" {
				w.WriteHeader(h)
				w.Write([]byte("{}"))
			}
			w.Close()
			f.Close()
			if _, err := VerifyBackup(file, ""); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestBackupTamperAndTerminalState(t *testing.T) {
	s, st := backupFixture(t)
	st.Status = "cancelled"
	s.Save(st)
	original := filepath.Join(t.TempDir(), "original.tar")
	report, err := s.Backup(original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyBackup(original, strings.Repeat("0", 64)); err == nil {
		t.Fatal("outer hash ignored")
	}
	corrupted := filepath.Join(t.TempDir(), "modified.tar")
	in, _ := os.Open(original)
	defer in.Close()
	out, _ := os.Create(corrupted)
	tr, tw := tar.NewReader(in), tar.NewWriter(out)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(tr)
		if h.Name == "workspace/data.txt" {
			data[0] = 'X'
		}
		tw.WriteHeader(h)
		tw.Write(data)
	}
	tw.Close()
	out.Close()
	if _, err = VerifyBackup(corrupted, ""); err == nil {
		t.Fatal("internal hash ignored")
	}
	destination := filepath.Join(t.TempDir(), "terminal")
	if _, err = RestoreBackup(original, report.SHA256, destination); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(destination, "state.json"))
	var restored State
	json.Unmarshal(b, &restored)
	if restored.Status != "cancelled" {
		t.Fatal("terminal state reversed")
	}
}
