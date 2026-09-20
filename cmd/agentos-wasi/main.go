// agentos-wasi is a one-job process; stdout contains only a validated task result.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/charlesmmorais/agentos-core/internal/wasiprotocol"
	"github.com/charlesmmorais/agentos-core/internal/wasiruntime"
	"io"
	"os"
)

func run() error {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, wasiprotocol.MaxEnvelope+1))
	if err != nil {
		return err
	}
	if len(raw) > wasiprotocol.MaxEnvelope {
		return errors.New("WASI job envelope exceeds limit")
	}
	var job wasiprotocol.Job
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&job); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("expected one WASI job")
	}
	result, err := wasiruntime.Run(context.Background(), job)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(result)
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
