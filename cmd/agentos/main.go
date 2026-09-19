package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/charlesmmorais/agentos-core/internal/core"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: agentos init|run|serve|status|pause|resume|cancel|attest [flags]")
	}
	action := os.Args[1]
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	dir := flags.String("state", "state", "state directory")
	workspace := flags.String("workspace", ".", "read-only analysis workspace")
	script := flags.String("script", "examples/analyze.py", "trusted Python script")
	mission := flags.String("mission", "Monitorar arquivos do workspace", "mission description")
	interval := flags.Int("interval", 5, "seconds between cycles")
	cycles := flags.Int("cycles", 3, "maximum cycles")
	listen := flags.String("listen", "127.0.0.1:8080", "loopback API address")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	s, err := core.Open(*dir)
	if err != nil {
		return err
	}
	defer s.Close()
	if action == "init" {
		if _, err = os.Stat(filepath.Join(*dir, "state.json")); !os.IsNotExist(err) {
			return errors.New("state already exists or is inaccessible")
		}
		w, err := filepath.Abs(*workspace)
		if err != nil {
			return err
		}
		p, err := filepath.Abs(*script)
		if err != nil {
			return err
		}
		st, err := core.New(core.Protocol{Mission: *mission, Workspace: w, Script: p, IntervalSeconds: *interval, TimeoutSeconds: 30, MaxCycles: *cycles})
		if err != nil {
			return err
		}
		st.Execution, err = core.Inspect(st.Protocol)
		if err != nil {
			return err
		}
		if err = s.Save(st); err != nil {
			return err
		}
		fmt.Println("initialized", st.AgentID)
		return nil
	}
	st, err := s.Load()
	if err != nil {
		return err
	}
	switch action {
	case "attest":
		if st.Pending != "" {
			return errors.New("pending cycle: restore the original script/environment or cancel this mission")
		}
		st.Execution, err = core.Inspect(st.Protocol)
		if err != nil {
			return err
		}
		st.Events = append(st.Events, core.Event{At: time.Now().UTC(), Kind: "attested", Cycle: st.Completed})
		return s.Save(st)
	case "serve":
		if st.Execution == nil {
			return errors.New("execution manifest missing; run attest")
		}
		return serve(s, st, *listen)
	case "status":
		b, _ := json.MarshalIndent(st, "", "  ")
		fmt.Println(string(b))
		return nil
	case "pause", "resume", "cancel":
		if st.Status == "cancelled" || st.Status == "completed" {
			return errors.New("terminal state cannot be changed")
		}
		statuses := map[string]string{"pause": "paused", "resume": "active", "cancel": "cancelled"}
		st.Status = statuses[action]
		st.Events = append(st.Events, core.Event{At: time.Now().UTC(), Kind: action, Cycle: st.Completed})
		return s.Save(st)
	case "run":
		if st.Execution == nil {
			return errors.New("execution manifest missing; run attest")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		for st.Status == "active" {
			if err = core.Tick(ctx, s, st, core.PinnedPython{Manifest: st.Execution}, time.Now().UTC()); err != nil {
				return err
			}
			fmt.Printf("agent=%s completed=%d status=%s\n", st.AgentID, st.Completed, st.Status)
			if st.Status != "active" {
				break
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
		}
		return nil
	default:
		return errors.New("unknown command")
	}
}
