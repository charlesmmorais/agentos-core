// Package wasiprotocol is the bounded wire contract for the isolated worker.
// It has no runtime dependency and can be imported by the portable core.
package wasiprotocol

import "encoding/json"

const (
	Policy      = "wasi-preview1-stdio-v1"
	MaxModule   = 16 << 20
	MaxInput    = 1 << 20
	MaxOutput   = 1 << 20
	MaxStderr   = 64 << 10
	MaxEnvelope = 24 << 20
	MemoryPages = 2048 // 128 MiB of guest linear memory, not host process RSS.
	MaxSeconds  = 300
)

type Job struct {
	Policy         string          `json:"policy"`
	Module         []byte          `json:"module"`
	SHA256         string          `json:"sha256"`
	Input          json.RawMessage `json:"input"`
	TimeoutSeconds int             `json:"timeout_seconds"`
}
