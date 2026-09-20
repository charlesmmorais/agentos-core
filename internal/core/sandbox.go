package core

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/charlesmmorais/agentos-core/internal/platform"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed agentos.py
var sandboxSDK []byte

//go:embed bootstrap.py
var sandboxBootstrap []byte

func validImage(s string) bool {
	if !strings.HasPrefix(s, "sha256:") || len(s) != 71 {
		return false
	}
	_, err := hex.DecodeString(s[7:])
	return err == nil
}
func InspectDocker(p Protocol, image string) (*Manifest, error) {
	if err := platform.Current().Require(platform.LinuxSandbox); err != nil {
		return nil, err
	}
	if !validImage(image) {
		return nil, errors.New("sandbox image must be a local immutable sha256 image ID")
	}
	source, err := os.ReadFile(p.Script)
	if err != nil {
		return nil, err
	}
	if len(source) > 64*1024 {
		return nil, errors.New("entry script exceeds 64 KiB")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", image)
	b, err := cmd.Output()
	if err != nil {
		return nil, errors.New("Docker/image unavailable; no host fallback")
	}
	if strings.TrimSpace(string(b)) != image {
		return nil, errors.New("image ID mismatch")
	}
	return &Manifest{Runtime: "docker", Image: image, ScriptSHA256: digest(source)}, nil
}

// snapshotWorkspace exports regular nonhidden files from the first directory
// level only. Never bind-mount operator data, symlinks or sockets into the guest.
func snapshotWorkspace(source, dest string) error {
	dir, err := os.Open(source)
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(1001)
	if err != nil && err != io.EOF {
		return err
	}
	if len(entries) > 1000 {
		return errors.New("workspace exceeds 1000 entries")
	}
	var total int64
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !entry.Type().IsRegular() {
			continue
		}
		file, err := regularFile(filepath.Join(source, entry.Name()))
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			file.Close()
			return errors.New("workspace entry changed type")
		}
		if info.Size() > 4*1024*1024 || total+info.Size() > 16*1024*1024 {
			file.Close()
			return errors.New("workspace export size limit exceeded")
		}
		b, err := io.ReadAll(io.LimitReader(file, 4*1024*1024+1))
		file.Close()
		if err != nil {
			return err
		}
		total += int64(len(b))
		if len(b) > 4*1024*1024 || total > 16*1024*1024 {
			return errors.New("workspace export size limit exceeded")
		}
		if err = os.WriteFile(filepath.Join(dest, entry.Name()), b, 0444); err != nil {
			return err
		}
	}
	return nil
}

func dockerArgs(name, image, stage string) []string {
	return []string{"run", "--rm", "--pull=never", "--name", name, "--label", "agentos.sandbox=" + name,
		"--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges=true", "--log-driver=none",
		"--user=65534:65534", "--pids-limit=32", "--memory=128m", "--memory-swap=128m", "--cpus=0.5",
		"--ulimit", "nofile=128:128", "--ulimit", "fsize=16777216:16777216",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=16m,mode=1777",
		"--mount", "type=bind,src=" + stage + ",dst=/input,readonly",
		"--workdir=/input/workspace", "--env", "PYTHONDONTWRITEBYTECODE=1",
		"--entrypoint=python3", "-i", image, "-I", "/input/bootstrap.py"}
}

type DockerPython struct{ Manifest *Manifest }

func (e DockerPython) Execute(ctx context.Context, p Protocol, r Request) (json.RawMessage, error) {
	if e.Manifest == nil || e.Manifest.Runtime != "docker" {
		return nil, errors.New("sandbox manifest required")
	}
	actual, err := InspectDocker(p, e.Manifest.Image)
	if err != nil {
		return nil, err
	}
	if *actual != *e.Manifest {
		return nil, errors.New("sandbox script or image changed")
	}
	stage, err := os.MkdirTemp("", "agentos-sandbox-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	if strings.Contains(stage, ",") {
		return nil, errors.New("unsupported staging path")
	}
	os.Chmod(stage, 0755)
	workspace := filepath.Join(stage, "workspace")
	if err = os.Mkdir(workspace, 0755); err != nil {
		return nil, err
	}
	if err = snapshotWorkspace(p.Workspace, workspace); err != nil {
		return nil, err
	}
	source, err := os.ReadFile(p.Script)
	if err != nil {
		return nil, err
	}
	if digest(source) != actual.ScriptSHA256 {
		return nil, errors.New("script changed")
	}
	if err = os.WriteFile(filepath.Join(stage, "script.py"), source, 0444); err != nil {
		return nil, err
	}
	if err = os.Mkdir(filepath.Join(stage, "sdk"), 0755); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(stage, "sdk", "agentos.py"), sandboxSDK, 0444); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(stage, "bootstrap.py"), sandboxBootstrap, 0444); err != nil {
		return nil, err
	}
	brokerDir := filepath.Join(stage, "broker")
	if err = os.Mkdir(brokerDir, 0755); err != nil {
		return nil, err
	}
	r.Workspace = "/input/workspace"
	closeBroker, err := startBroker(brokerDir, r)
	if err != nil {
		return nil, err
	}
	defer closeBroker()
	name := "agentos-" + digest([]byte(r.AgentID))[:32]
	// A single local state owner may reclaim only its specifically labelled container.
	clean := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		inspect := exec.CommandContext(cleanupCtx, "docker", "inspect", "--format", `{{index .Config.Labels "agentos.sandbox"}}`, name)
		label, inspectErr := inspect.Output()
		if inspectErr != nil {
			return nil
		}
		if strings.TrimSpace(string(label)) != name {
			return errors.New("container name collision")
		}
		return exec.CommandContext(cleanupCtx, "docker", "rm", "-f", name).Run()
	}
	if err = clean(); err != nil {
		return nil, err
	}
	defer clean()
	runctx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(runctx, "docker", dockerArgs(name, actual.Image, stage)...)
	cmd.Stdin = bytes.NewReader(b)
	var out, stderr limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("sandbox execution failed: %w", err)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(out.Bytes(), &object) != nil || object == nil {
		return nil, errors.New("sandbox must return a JSON object")
	}
	return append(json.RawMessage(nil), out.Bytes()...), nil
}
