package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

func main() {
	var r struct {
		Address   string `json:"address"`
		Mission   string `json:"mission"`
		CycleID   string `json:"cycle_id"`
		Workspace string `json:"workspace"`
	}
	if json.NewDecoder(os.Stdin).Decode(&r) != nil {
		os.Exit(2)
	}
	switch r.Mission {
	case "delay":
		time.Sleep(time.Second)
		json.NewEncoder(os.Stdout).Encode(map[string]string{"cycle_id": r.CycleID})
	case "loop":
		for {
		}
	case "memory":
		b := make([]byte, 256<<20)
		b[len(b)-1] = 1
		json.NewEncoder(os.Stdout).Encode(map[string]int{"n": len(b)})
	case "output":
		io.WriteString(os.Stdout, strings.Repeat("x", (1<<20)+1))
	case "stderr":
		io.WriteString(os.Stderr, strings.Repeat("x", (64<<10)+1))
		io.WriteString(os.Stdout, `{}`)
	case "invalid":
		io.WriteString(os.Stdout, `[]`)
	case "trailing":
		io.WriteString(os.Stdout, `{} {}`)
	case "exit":
		os.Exit(7)
	case "probe":
		_, readErr := os.ReadFile(r.Workspace + "/secret")
		writeErr := os.WriteFile(r.Workspace+"/written", []byte("bad"), 0600)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		c, netErr := (&net.Dialer{}).DialContext(ctx, "tcp", r.Address)
		if c != nil {
			c.Close()
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"read_denied": readErr != nil, "write_denied": writeErr != nil, "network_denied": netErr != nil, "secret_env": os.Getenv("AGENTOS_TEST_SECRET"), "cycle_id": r.CycleID})
	default:
		json.NewEncoder(os.Stdout).Encode(map[string]string{"cycle_id": r.CycleID})
	}
}
