package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/charlesmmorais/agentos-core/internal/platform"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Manifest is an inventory, not a signature or a full dependency-content lock.
type Manifest struct {
	WASIHelper        string `json:"wasi_helper,omitempty"`
	WASIHelperSHA256  string `json:"wasi_helper_sha256,omitempty"`
	WASIPolicy        string `json:"wasi_policy,omitempty"`
	Runtime           string `json:"runtime,omitempty"`
	Image             string `json:"image,omitempty"`
	ScriptSHA256      string `json:"script_sha256"`
	Python            string `json:"python"`
	PythonSHA256      string `json:"python_sha256"`
	EnvironmentSHA256 string `json:"environment_sha256"`
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func Inspect(p Protocol) (*Manifest, error) {
	if err := platform.Current().Require(platform.NativeProcess); err != nil {
		return nil, err
	}
	source, err := os.ReadFile(p.Script)
	if err != nil {
		return nil, err
	}
	if len(source) > 64*1024 {
		return nil, errors.New("entry script exceeds 64 KiB")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return nil, err
	}
	python, err = filepath.Abs(python)
	if err != nil {
		return nil, err
	}
	binary, err := os.ReadFile(python)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-I", "-c", `import sys,json,importlib.metadata as m; print(json.dumps([sys.version,sys.prefix,sys.base_prefix,sorted((d.metadata.get('Name',''),d.version) for d in m.distributions())]))`)
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}
	var output limitedBuffer
	cmd.Stdout = &output
	if err = cmd.Run(); err != nil {
		return nil, err
	}
	return &Manifest{ScriptSHA256: digest(source), Python: python, PythonSHA256: digest(binary), EnvironmentSHA256: digest(output.Bytes())}, nil
}

func SelectExecutor(m *Manifest) Executor {
	if m != nil && m.Runtime == "wasi" {
		return WASI{Manifest: m}
	}
	if m != nil && m.Runtime == "docker" {
		return DockerPython{Manifest: m}
	}
	if m != nil && m.Runtime != "" {
		return rejectedExecutor{}
	}
	return PinnedPython{Manifest: m}
}

type rejectedExecutor struct{}

func (rejectedExecutor) Execute(context.Context, Protocol, Request) (json.RawMessage, error) {
	return nil, errors.New("unsupported executor; no fallback")
}

type PinnedPython struct{ Manifest *Manifest }

func (e PinnedPython) Execute(ctx context.Context, p Protocol, r Request) (json.RawMessage, error) {
	if e.Manifest == nil {
		return nil, errors.New("execution manifest missing; run attest explicitly")
	}
	actual, err := Inspect(p)
	if err != nil {
		return nil, err
	}
	if *actual != *e.Manifest {
		return nil, errors.New("script or Python environment changed; execution blocked")
	}
	source, err := os.ReadFile(p.Script)
	if err != nil {
		return nil, err
	}
	if digest(source) != actual.ScriptSHA256 {
		return nil, errors.New("script changed during verification")
	}
	return executePython(ctx, p, r, actual.Python, []string{"-I", "-c", `import sys; __file__=sys.argv[1]; exec(compile(sys.argv[2],__file__,'exec'))`, p.Script, string(source)})
}

type Artifact struct {
	Cycle  int    `json:"cycle"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

func (s *Store) PutArtifact(cycle int, b []byte) (Artifact, error) {
	a := Artifact{cycle, digest(b), len(b)}
	dir := filepath.Join(s.Dir, "artifacts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return a, err
	}
	f, err := os.CreateTemp(dir, ".artifact-*")
	if err != nil {
		return a, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return a, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return a, err
	}
	if err = f.Close(); err != nil {
		return a, err
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, a.SHA256+".json")); err != nil {
		return a, err
	}
	d, err := os.Open(dir)
	if err != nil {
		return a, err
	}
	defer d.Close()
	return a, d.Sync()
}
func (s *Store) ReadArtifact(a Artifact) (json.RawMessage, error) {
	if len(a.SHA256) != 64 {
		return nil, errors.New("invalid artifact hash")
	}
	if _, err := hex.DecodeString(a.SHA256); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, "artifacts", a.SHA256+".json"))
	if err != nil {
		return nil, err
	}
	if digest(b) != a.SHA256 || len(b) != a.Bytes {
		return nil, errors.New("artifact integrity mismatch")
	}
	return b, nil
}
