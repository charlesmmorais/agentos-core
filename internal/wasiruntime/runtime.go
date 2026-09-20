// Package wasiruntime runs WASI Preview 1 without host filesystem or network grants.
// This package is linked only into the separate agentos-wasi worker.
package wasiruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/charlesmmorais/agentos-core/internal/wasiprotocol"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type output struct {
	bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *output) Write(p []byte) (int, error) {
	if b.exceeded || len(p) > b.limit-b.Len() {
		b.exceeded = true
		b.cancel()
		return 0, errors.New("WASI output limit exceeded")
	}
	return b.Buffer.Write(p)
}

func Run(ctx context.Context, job wasiprotocol.Job) (json.RawMessage, error) {
	if job.Policy != wasiprotocol.Policy || job.TimeoutSeconds < 1 || job.TimeoutSeconds > wasiprotocol.MaxSeconds {
		return nil, errors.New("unsupported WASI policy or timeout")
	}
	if len(job.Module) == 0 || len(job.Module) > wasiprotocol.MaxModule || len(job.Input) > wasiprotocol.MaxInput {
		return nil, errors.New("WASI module or input exceeds limit")
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(job.Input, &input) != nil || input == nil {
		return nil, errors.New("WASI input must be a JSON object")
	}
	sum := sha256.Sum256(job.Module)
	if hex.EncodeToString(sum[:]) != job.SHA256 {
		return nil, errors.New("WASI module SHA-256 mismatch")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
	defer cancel()
	// Interpreter avoids executable-memory/JIT requirements and is portable.
	cfg := wazero.NewRuntimeConfigInterpreter().WithMemoryLimitPages(wasiprotocol.MemoryPages).WithCloseOnContextDone(true)
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	defer rt.Close(context.Background())
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return nil, err
	}
	compiled, err := rt.CompileModule(ctx, job.Module)
	if err != nil {
		return nil, fmt.Errorf("compile WASI: %w", err)
	}
	defer compiled.Close(context.Background())
	if compiled.ExportedFunctions()["_start"] == nil {
		return nil, errors.New("WASI command requires _start")
	}
	// Only Preview 1 functions can link; there are no custom action/network imports.
	for _, fn := range compiled.ImportedFunctions() {
		module, _, _ := fn.Import()
		if module != "wasi_snapshot_preview1" {
			return nil, errors.New("unsupported host import")
		}
	}
	out := &output{limit: wasiprotocol.MaxOutput, cancel: cancel}
	stderr := &output{limit: wasiprotocol.MaxStderr, cancel: cancel}
	// No WithFSConfig, WithEnv, socket configuration or inherited descriptors.
	mc := wazero.NewModuleConfig().WithName("task").WithStdin(bytes.NewReader(job.Input)).WithStdout(out).WithStderr(stderr).WithSysWalltime().WithSysNanotime().WithSysNanosleep()
	_, err = rt.InstantiateModule(ctx, compiled, mc)
	if out.exceeded || stderr.exceeded {
		return nil, errors.New("WASI output limit exceeded")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("execute WASI: %w", err)
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(out.Bytes(), &result) != nil || result == nil {
		return nil, errors.New("WASI task must return one JSON object")
	}
	return append(json.RawMessage(nil), out.Bytes()...), nil
}
