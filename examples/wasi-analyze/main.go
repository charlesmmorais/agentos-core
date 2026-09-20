// Build: GOOS=wasip1 GOARCH=wasm go build -o analyze.wasm ./examples/wasi-analyze
// Input is the AgentOS cycle request. No workspace or host service is accessed.
package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"unicode"
)

func main() {
	var request struct {
		Mission string `json:"mission"`
		CycleID string `json:"cycle_id"`
	}
	if json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&request) != nil {
		os.Exit(1)
	}
	words := strings.FieldsFunc(strings.ToLower(request.Mission), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	counts := map[string]int{}
	for _, word := range words {
		counts[word]++
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"schema": "agentos.word-frequency.v1", "cycle_id": request.CycleID, "word_count": len(words), "unique_words": len(counts), "frequencies": counts})
}
