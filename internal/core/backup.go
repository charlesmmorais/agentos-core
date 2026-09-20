package core

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/charlesmmorais/agentos-core/internal/platform"
	"github.com/charlesmmorais/agentos-core/internal/wasiprotocol"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const maxBackupBytes = 256 * 1024 * 1024
const maxArchiveBytes = 512 * 1024 * 1024

type backupEntry struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type backupManifest struct {
	Format    string        `json:"format"`
	AgentID   string        `json:"agent_id"`
	CreatedAt time.Time     `json:"created_at"`
	Files     []backupEntry `json:"files"`
}
type BackupReport struct {
	SHA256    string    `json:"sha256"`
	AgentID   string    `json:"agent_id"`
	Status    string    `json:"original_status"`
	Completed int       `json:"completed"`
	Files     int       `json:"files"`
	Bytes     int       `json:"payload_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

func boundedFile(path string, limit int) ([]byte, error) {
	f, err := regularFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, errors.New("backup file size limit exceeded")
	}
	return b, nil
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func backupLimit(name string) int {
	switch name {
	case "state.json":
		return 32 * 1024 * 1024
	case "module.wasm":
		return wasiprotocol.MaxModule
	case "script.py":
		return 64 * 1024
	case "manifest.json":
		return 4 * 1024 * 1024
	}
	if !utf8.ValidString(name) || strings.ContainsAny(name, "\\\x00") {
		return 0
	}
	parts := strings.Split(name, "/")
	if len(parts) != 2 || parts[1] == "" || strings.HasPrefix(parts[1], ".") {
		return 0
	}
	if parts[0] == "workspace" {
		return 4 * 1024 * 1024
	}
	if parts[0] == "artifacts" && strings.HasSuffix(parts[1], ".json") {
		h := strings.TrimSuffix(parts[1], ".json")
		if b, err := hex.DecodeString(h); err == nil && len(b) == 32 && h == strings.ToLower(h) {
			return 1024 * 1024
		}
	}
	return 0
}

// Caller owns the Store flock throughout. The bundle excludes lock files,
// orphan artifacts, credentials and executables/images from the host runtime.
func (s *Store) Backup(output string) (*BackupReport, error) {
	b, err := boundedFile(filepath.Join(s.Dir, "state.json"), 32*1024*1024)
	if err != nil {
		return nil, err
	}
	st, err := decodeState(b)
	if err != nil {
		return nil, err
	}
	if st.Execution == nil {
		return nil, errors.New("backup requires an execution manifest")
	}
	script, err := boundedFile(st.Protocol.Script, backupLimit(executionBackupName(st.Execution)))
	if err != nil {
		return nil, err
	}
	if digest(script) != st.Execution.ScriptSHA256 {
		return nil, errors.New("script no longer matches manifest; backup aborted")
	}
	workspace, err := os.MkdirTemp("", "agentos-backup-input-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workspace)
	if err = snapshotWorkspace(st.Protocol.Workspace, workspace); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Dir(output), ".agentos-backup-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	hash := sha256.New()
	writer := tar.NewWriter(io.MultiWriter(f, hash))
	manifest := backupManifest{Format: "agentos.backup.v1", AgentID: st.AgentID, CreatedAt: time.Now().UTC()}
	if st.Execution.Runtime == "wasi" {
		manifest.Format = "agentos.backup.v2"
	}
	total := 0
	add := func(name string, data []byte) error {
		if backupLimit(name) == 0 || len(data) > backupLimit(name) || total+len(data) > maxBackupBytes || len(manifest.Files) >= 12004 {
			return errors.New("backup size, count or path limit exceeded")
		}
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg, ModTime: manifest.CreatedAt}); err != nil {
			return err
		}
		if _, err := writer.Write(data); err != nil {
			return err
		}
		if name != "manifest.json" {
			total += len(data)
			manifest.Files = append(manifest.Files, backupEntry{name, len(data), digest(data)})
		}
		return nil
	}
	if err = add("state.json", b); err != nil {
		return nil, err
	}
	if err = add(executionBackupName(st.Execution), script); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		data, err := boundedFile(filepath.Join(workspace, entry.Name()), 4*1024*1024)
		if err != nil {
			return nil, err
		}
		if err = add("workspace/"+entry.Name(), data); err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	for _, a := range st.Artifacts {
		name := "artifacts/" + a.SHA256 + ".json"
		if backupLimit(name) == 0 {
			return nil, errors.New("invalid artifact path")
		}
		data, err := boundedFile(filepath.Join(s.Dir, name), 1024*1024)
		if err != nil {
			return nil, err
		}
		if len(data) != a.Bytes || digest(data) != a.SHA256 {
			return nil, errors.New("artifact integrity mismatch")
		}
		if !seen[name] {
			if err = add(name, data); err != nil {
				return nil, err
			}
			seen[name] = true
		}
	}
	meta, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if err = add("manifest.json", meta); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	if err = f.Sync(); err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	// Hard-link publication is atomic and refuses any existing destination.
	if err = os.Link(f.Name(), output); err != nil {
		return nil, err
	}
	if err = syncDir(filepath.Dir(output)); err != nil {
		return nil, err
	}
	return &BackupReport{hex.EncodeToString(hash.Sum(nil)), st.AgentID, st.Status, st.Completed, len(manifest.Files), total, manifest.CreatedAt}, nil
}

// unpack validates a bounded archive into a private staging directory. Names
// are allowlisted, and no links, special files, duplicate or extra artifacts pass.
func unpack(archive, expected, stage string) (*State, *BackupReport, error) {
	f, err := regularFile(archive)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	limit := &io.LimitedReader{R: f, N: maxArchiveBytes + 1}
	hash := sha256.New()
	reader := io.TeeReader(limit, hash)
	tr := tar.NewReader(reader)
	actual := map[string]backupEntry{}
	var manifest backupManifest
	metaSeen := false
	metadataBytes, entriesRead := 0, 0
	total, workspaceBytes, workspaceFiles := 0, 0, 0
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		name := header.Name
		entriesRead++
		if entriesRead > 12004 {
			return nil, nil, errors.New("archive entry count exceeded")
		}
		cap := backupLimit(name)
		if cap == 0 || header.Size < 0 || header.Size > int64(cap) || (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) || len(actual) >= 12004 {
			return nil, nil, errors.New("unsafe archive entry")
		}
		if _, ok := actual[name]; ok {
			return nil, nil, errors.New("duplicate archive entry")
		}
		data, err := io.ReadAll(tr)
		if err != nil || len(data) != int(header.Size) {
			return nil, nil, errors.New("truncated archive entry")
		}
		if name == "manifest.json" {
			if metaSeen || json.Unmarshal(data, &manifest) != nil {
				return nil, nil, errors.New("invalid backup manifest")
			}
			metaSeen = true
			metadataBytes = len(data)
			continue
		}
		total += len(data)
		if total > maxBackupBytes {
			return nil, nil, errors.New("backup exceeds payload limit")
		}
		if strings.HasPrefix(name, "workspace/") {
			workspaceFiles++
			workspaceBytes += len(data)
			if workspaceFiles > 1000 || workspaceBytes > 16*1024*1024 {
				return nil, nil, errors.New("backup workspace exceeds export limits")
			}
		}
		path := filepath.Join(stage, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, nil, err
		}
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, nil, err
		}
		_, err = out.Write(data)
		if err == nil {
			err = out.Sync()
		}
		closeErr := out.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, nil, err
		}
		actual[name] = backupEntry{name, len(data), digest(data)}
	}
	if total+metadataBytes > maxBackupBytes {
		return nil, nil, errors.New("archive payload and inventory exceed limit")
	}
	padding := make([]byte, 32768)
	for {
		n, readErr := reader.Read(padding)
		for _, v := range padding[:n] {
			if v != 0 {
				return nil, nil, errors.New("unexpected data after archive end")
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, nil, readErr
		}
	}
	if limit.N == 0 {
		return nil, nil, errors.New("archive exceeds limit")
	}
	archiveHash := hex.EncodeToString(hash.Sum(nil))
	if expected != "" && expected != archiveHash {
		return nil, nil, errors.New("archive SHA-256 mismatch")
	}
	if !metaSeen || (manifest.Format != "agentos.backup.v1" && manifest.Format != "agentos.backup.v2") || len(manifest.Files) != len(actual) {
		return nil, nil, errors.New("backup manifest mismatch")
	}
	listed := map[string]bool{}
	for _, entry := range manifest.Files {
		got, exists := actual[entry.Path]
		if !exists || listed[entry.Path] || got != entry {
			return nil, nil, errors.New("backup entry hash or inventory mismatch")
		}
		listed[entry.Path] = true
	}
	stateBytes, err := boundedFile(filepath.Join(stage, "state.json"), 32*1024*1024)
	if err != nil {
		return nil, nil, err
	}
	st, err := decodeState(stateBytes)
	if err != nil {
		return nil, nil, err
	}
	if st.AgentID != manifest.AgentID || st.Execution == nil {
		return nil, nil, errors.New("backup identity or execution manifest missing")
	}
	if (st.Execution.Runtime == "wasi") != (manifest.Format == "agentos.backup.v2") {
		return nil, nil, errors.New("backup format does not match runtime")
	}
	if script, exists := actual[executionBackupName(st.Execution)]; !exists || script.SHA256 != st.Execution.ScriptSHA256 {
		return nil, nil, errors.New("backed-up script integrity mismatch")
	}
	referenced := map[string]bool{}
	for _, a := range st.Artifacts {
		name := "artifacts/" + a.SHA256 + ".json"
		entry, ok := actual[name]
		if !ok || entry.SHA256 != a.SHA256 || entry.Bytes != a.Bytes {
			return nil, nil, errors.New("referenced artifact missing or corrupt")
		}
		referenced[name] = true
	}
	for name := range actual {
		if (name == "script.py" || name == "module.wasm") && name != executionBackupName(st.Execution) {
			return nil, nil, errors.New("unexpected executable in archive")
		}
		if strings.HasPrefix(name, "artifacts/") && !referenced[name] {
			return nil, nil, errors.New("unreferenced archive artifact")
		}
	}
	return st, &BackupReport{archiveHash, st.AgentID, st.Status, st.Completed, len(actual), total, manifest.CreatedAt}, nil
}

func VerifyBackup(archive, expected string) (*BackupReport, error) {
	stage, err := os.MkdirTemp("", "agentos-verify-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	_, report, err := unpack(archive, expected, stage)
	return report, err
}
func RestoreBackup(archive, expected, destination string) (*BackupReport, error) {
	if err := platform.Current().Require(platform.DurableState); err != nil {
		return nil, err
	}
	if b, err := hex.DecodeString(expected); err != nil || len(b) != 32 {
		return nil, errors.New("restore requires --sha256 from the reviewed backup receipt")
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	if _, err = os.Lstat(destination); !os.IsNotExist(err) {
		return nil, errors.New("restore destination must not exist")
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".agentos-restore-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	st, report, err := unpack(archive, expected, stage)
	if err != nil {
		return nil, err
	}
	st.Recovery = &RecoveryState{report.SHA256, st.Status, time.Now().UTC(), false}
	if st.Status == "active" {
		st.Status = "paused"
	}
	st.Protocol.Script = filepath.Join(destination, executionBackupName(st.Execution))
	st.Protocol.Workspace = filepath.Join(destination, "workspace")
	for i := range st.Actions {
		a := &st.Actions[i]
		switch a.Status {
		case "approved", "in_flight", "unknown", "retry_required":
			a.Status = "unknown"
			a.LastError = "restored backup; reconcile against destination before any retry"
			a.UpdatedAt = time.Now().UTC()
		}
	}
	st.Events = append(st.Events, Event{At: time.Now().UTC(), Kind: "restored_paused_review_required", Cycle: st.Completed})
	if err = (&Store{Dir: stage}).Save(st); err != nil {
		return nil, err
	}
	for _, name := range []string{"workspace", "artifacts"} {
		path := filepath.Join(stage, name)
		if err = os.MkdirAll(path, 0700); err != nil {
			return nil, err
		}
		if err = syncDir(path); err != nil {
			return nil, err
		}
	}
	if err = syncDir(stage); err != nil {
		return nil, err
	}
	// Reserve a new directory and hold its writer lock. Link all dependencies
	// first, then publish state.json as the commit marker. A crash before that
	// leaves an incomplete directory that cannot load; no existing file is replaced.
	if err = os.Mkdir(destination, 0700); err != nil {
		return nil, err
	}
	owner, err := Open(destination)
	if err != nil {
		return nil, err
	}
	defer owner.Close()
	err = filepath.WalkDir(stage, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		if relative == "." || relative == "state.json" {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.Mkdir(target, 0700)
		}
		return os.Link(path, target)
	})
	if err != nil {
		return nil, fmt.Errorf("incomplete restore directory; inspect before removal: %w", err)
	}
	for _, name := range []string{"workspace", "artifacts"} {
		if err = syncDir(filepath.Join(destination, name)); err != nil {
			return nil, err
		}
	}
	if err = syncDir(destination); err != nil {
		return nil, err
	}
	if err = os.Link(filepath.Join(stage, "state.json"), filepath.Join(destination, "state.json")); err != nil {
		return nil, err
	}
	if err = syncDir(destination); err != nil {
		return nil, err
	}
	if err = syncDir(filepath.Dir(destination)); err != nil {
		return nil, err
	}
	return report, nil
}
