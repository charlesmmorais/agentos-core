package core

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// A broker is scoped to one execution and exposes snapshots, not the state store.
// Its socket is never mounted alongside the operator API or Docker socket.
func startBroker(dir string, r Request) (func(), error) {
	listener, err := net.Listen("unix", filepath.Join(dir, "broker.sock"))
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(filepath.Join(dir, "broker.sock"), 0666); err != nil {
		listener.Close()
		return nil, err
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			default:
				conn.Close()
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-slots }()
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(2 * time.Second))
				scan := bufio.NewScanner(conn)
				scan.Buffer(make([]byte, 1024), 4096)
				if !scan.Scan() {
					return
				}
				var request struct {
					Method string `json:"method"`
				}
				response := map[string]any{"error": "capability_denied"}
				if json.Unmarshal(scan.Bytes(), &request) == nil {
					switch request.Method {
					case "memory.read":
						response = map[string]any{"result": r.Memory}
					case "context.get":
						response = map[string]any{"result": map[string]string{"agent_id": r.AgentID, "cycle_id": r.CycleID, "mission": r.Mission}}
					}
				}
				json.NewEncoder(conn).Encode(response)
			}()
		}
	}()
	return func() { listener.Close(); wg.Wait() }, nil
}
