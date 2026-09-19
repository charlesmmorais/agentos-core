package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/charlesmmorais/agentos-core/internal/core"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func serve(s *core.Store, st *core.State, address string) error {
	token := os.Getenv("AGENTOS_API_TOKEN")
	if len(token) < 32 {
		return errors.New("AGENTOS_API_TOKEN must contain at least 32 characters")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("API must bind to a literal loopback address")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	controller := core.NewController(s, st, core.PinnedPython{Manifest: st.Execution})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Handler: controller.Handler(token), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if ctx.Err() != nil {
				return
			}
			if err := controller.Step(ctx, time.Now().UTC()); err != nil {
				fmt.Fprintln(os.Stderr, "execution blocked; inspect state and local configuration")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
	go func() { <-ctx.Done(); server.Close() }()
	fmt.Println("API listening on", listener.Addr())
	err = server.Serve(listener)
	stop()
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
