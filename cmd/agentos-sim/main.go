// agentos-sim validates a protocol from stdin without executing host operations.
package main

import (
	"encoding/json"
	"fmt"
	"github.com/charlesmmorais/agentos-core/internal/kernel"
	"io"
	"os"
)

func main() {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	if err != nil {
		fail(err)
	}
	p, err := kernel.DecodeProtocol(raw)
	if err != nil {
		fail(err)
	}
	result, err := kernel.Simulate(p)
	if err != nil {
		fail(err)
	}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fail(err)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
