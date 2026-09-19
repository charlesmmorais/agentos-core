package core

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxPolicy(t *testing.T) {
	args := strings.Join(dockerArgs("agentos-test", "sha256:"+strings.Repeat("a", 64), "/tmp/stage"), " ")
	for _, required := range []string{"--network=none", "--read-only", "--cap-drop=ALL", "--memory=128m", "--memory-swap=128m", "--pids-limit=32", "--cpus=0.5", "--user=65534:65534", "--pull=never", "no-new-privileges", "--log-driver=none"} {
		if !strings.Contains(args, required) {
			t.Fatal(required)
		}
	}
	if validImage("python:latest") || validImage("sha256:../escape") {
		t.Fatal("mutable or invalid image accepted")
	}
	_, err := SelectExecutor(&Manifest{Runtime: "unknown"}).Execute(context.Background(), Protocol{}, Request{})
	if err == nil {
		t.Fatal("unknown runtime fallback")
	}
}
func TestWorkspaceExport(t *testing.T) {
	source, dest := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(source, "safe.txt"), []byte("safe"), 0600)
	os.WriteFile(filepath.Join(source, ".secret"), []byte("secret"), 0600)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("outside"), 0600)
	os.Symlink(outside, filepath.Join(source, "link"))
	os.Mkdir(filepath.Join(source, "nested"), 0700)
	if err := snapshotWorkspace(source, dest); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 1 || entries[0].Name() != "safe.txt" {
		t.Fatal("exported unexpected files")
	}
}
func TestBrokerScope(t *testing.T) {
	dir := t.TempDir()
	closeBroker, err := startBroker(dir, Request{AgentID: "agent-a", Memory: json.RawMessage(`{"ok":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	defer closeBroker()
	for _, method := range []string{"memory.read", "context.get", "tools.execute", "memory.write"} {
		conn, err := net.Dial("unix", filepath.Join(dir, "broker.sock"))
		if err != nil {
			t.Fatal(err)
		}
		json.NewEncoder(conn).Encode(map[string]string{"method": method})
		var response map[string]json.RawMessage
		err = json.NewDecoder(bufio.NewReader(conn)).Decode(&response)
		conn.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(method, "tools") || method == "memory.write" {
			if response["error"] == nil {
				t.Fatal("capability escaped")
			}
		} else if response["result"] == nil {
			t.Fatal("read denied")
		}
	}
}
