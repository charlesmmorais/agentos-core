package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/charlesmmorais/agentos-core/internal/platform"
	"github.com/charlesmmorais/agentos-core/internal/wasiprotocol"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func InspectWASI(p Protocol, helper string) (*Manifest, error) {
	if err := platform.Current().Require(platform.NativeProcess); err != nil {
		return nil, err
	}
	if err := validateHostProtocol(p); err != nil {
		return nil, err
	}
	if p.TimeoutSeconds > wasiprotocol.MaxSeconds {
		return nil, errors.New("WASI timeout must be 1..300 seconds")
	}
	module, err := boundedFile(p.Script, wasiprotocol.MaxModule)
	if err != nil {
		return nil, err
	}
	if len(module) < 8 || !bytes.Equal(module[:8], []byte{0, 97, 115, 109, 1, 0, 0, 0}) {
		return nil, errors.New("expected a WebAssembly v1 module")
	}
	if helper == "" {
		return nil, errors.New("WASI requires an explicit --wasi-helper path")
	}
	helper, err = filepath.Abs(helper)
	if err != nil {
		return nil, err
	}
	helper, err = filepath.EvalSymlinks(helper)
	if err != nil {
		return nil, err
	}
	binary, err := boundedFile(helper, 64<<20)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(helper)
	if err != nil {
		return nil, err
	}
	if info.Mode()&0111 == 0 {
		return nil, errors.New("WASI helper is not executable")
	}
	return &Manifest{Runtime: "wasi", ScriptSHA256: digest(module), WASIHelper: helper, WASIHelperSHA256: digest(binary), WASIPolicy: wasiprotocol.Policy}, nil
}

type WASI struct{ Manifest *Manifest }

func (e WASI) Execute(ctx context.Context, p Protocol, r Request) (json.RawMessage, error) {
	if e.Manifest == nil || e.Manifest.Runtime != "wasi" {
		return nil, errors.New("WASI manifest required")
	}
	actual, err := InspectWASI(p, e.Manifest.WASIHelper)
	if err != nil {
		return nil, err
	}
	if *actual != *e.Manifest {
		return nil, errors.New("WASI module, helper or policy changed; execution blocked")
	}
	// Execute exactly the bytes verified here, never reopen the module in the worker.
	module, err := boundedFile(p.Script, wasiprotocol.MaxModule)
	if err != nil {
		return nil, err
	}
	if digest(module) != actual.ScriptSHA256 {
		return nil, errors.New("WASI module changed during verification")
	}
	input, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if len(input) > wasiprotocol.MaxInput {
		return nil, errors.New("WASI input exceeds 1 MiB")
	}
	job, err := json.Marshal(wasiprotocol.Job{Policy: actual.WASIPolicy, Module: module, SHA256: actual.ScriptSHA256, Input: input, TimeoutSeconds: p.TimeoutSeconds})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	// A private working directory does not grant the guest filesystem access.
	stage, err := os.MkdirTemp("", "agentos-wasi-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	cmd := exec.CommandContext(ctx, actual.WASIHelper)
	cmd.Dir = stage
	cmd.Env = []string{}
	cmd.Stdin = bytes.NewReader(job)
	var out, stderr limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	if err = configureProcess(cmd); err != nil {
		return nil, err
	}
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("WASI worker failed: %w", err)
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(out.Bytes(), &result) != nil || result == nil {
		return nil, errors.New("WASI worker must return one JSON object")
	}
	return append(json.RawMessage(nil), out.Bytes()...), nil
}

func executionBackupName(m *Manifest) string {
	if m != nil && m.Runtime == "wasi" {
		return "module.wasm"
	}
	return "script.py"
}
