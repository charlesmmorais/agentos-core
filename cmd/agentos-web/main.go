//go:build js && wasm

package main

import (
	"encoding/json"
	"github.com/charlesmmorais/agentos-core/internal/kernel"
	"syscall/js"
)

func main() {
	f := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) != 1 || args[0].Type() != js.TypeString {
			return `{"error":"expected protocol JSON string"}`
		}
		raw := args[0].String()
		if len(raw) > 65536 {
			return `{"error":"protocol too large"}`
		}
		p, err := kernel.DecodeProtocol([]byte(raw))
		var result map[string]any
		if err == nil {
			result, err = kernel.Simulate(p)
		}
		if err != nil {
			result = map[string]any{"error": err.Error()}
		}
		b, _ := json.Marshal(result)
		return string(b)
	})
	defer f.Release()
	js.Global().Set("agentosSimulate", f)
	select {}
}
