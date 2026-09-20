package wasiruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/charlesmmorais/agentos-core/internal/wasiprotocol"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func module(t *testing.T) []byte {
	t.Helper()
	file := filepath.Join(t.TempDir(), "task.wasm")
	cmd := exec.Command("go", "build", "-o", file, "./testdata/task")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %s: %v", out, err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func job(b []byte, mission, workspace string) wasiprotocol.Job {
	sum := sha256.Sum256(b)
	input, _ := json.Marshal(map[string]string{"mission": mission, "cycle_id": "stable:1", "workspace": workspace})
	return wasiprotocol.Job{Policy: wasiprotocol.Policy, Module: b, SHA256: hex.EncodeToString(sum[:]), Input: input, TimeoutSeconds: 10}
}
func TestWASIIsolationAndLimits(t *testing.T) {
	b := module(t)
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "secret"), []byte("host secret"), 0600)
	t.Setenv("AGENTOS_TEST_SECRET", "credential")
	t.Run("capabilities", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		j := job(b, "probe", workspace)
		input, _ := json.Marshal(map[string]string{"mission": "probe", "cycle_id": "stable:1", "workspace": workspace, "address": listener.Addr().String()})
		j.Input = input
		out, err := Run(context.Background(), j)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if json.Unmarshal(out, &result) != nil {
			t.Fatal(string(out))
		}
		for _, key := range []string{"read_denied", "write_denied", "network_denied"} {
			if result[key] != true {
				t.Fatal(result)
			}
		}
		if result["secret_env"] != "" || result["cycle_id"] != "stable:1" {
			t.Fatal(result)
		}
		if _, err := os.Stat(filepath.Join(workspace, "written")); !os.IsNotExist(err) {
			t.Fatal("host was modified")
		}
	})
	for _, mode := range []string{"memory", "output", "stderr", "invalid", "trailing", "exit"} {
		t.Run(mode, func(t *testing.T) {
			if _, err := Run(context.Background(), job(b, mode, workspace)); err == nil {
				t.Fatal("accepted invalid task result")
			}
		})
	}
	t.Run("timeout", func(t *testing.T) {
		j := job(b, "loop", workspace)
		j.TimeoutSeconds = 1
		start := time.Now()
		if _, err := Run(context.Background(), j); err == nil {
			t.Fatal("loop survived")
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("cancellation was not bounded")
		}
	})
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Run(ctx, job(b, "probe", workspace)); err == nil {
			t.Fatal("ignored cancellation")
		}
	})
	t.Run("hash", func(t *testing.T) {
		j := job(b, "probe", workspace)
		j.Module = append([]byte(nil), b...)
		j.Module[len(j.Module)-1] ^= 1
		if _, err := Run(context.Background(), j); err == nil {
			t.Fatal("accepted changed module")
		}
	})
	t.Run("input", func(t *testing.T) {
		j := job(b, "probe", workspace)
		j.Input = make([]byte, wasiprotocol.MaxInput+1)
		if _, err := Run(context.Background(), j); err == nil {
			t.Fatal("accepted oversized input")
		}
	})
	t.Run("policy", func(t *testing.T) {
		j := job(b, "probe", workspace)
		j.Policy = "allow-host"
		if _, err := Run(context.Background(), j); err == nil {
			t.Fatal("accepted unknown policy")
		}
	})
}
