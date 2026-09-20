// Package platform reports implemented capabilities, not runtime health.
package platform

import (
	"fmt"
	"runtime"
)

type Capability string

const (
	NativeProcess Capability = "native_process"
	DurableState  Capability = "durable_state"
	LinuxSandbox  Capability = "linux_sandbox"
	Simulation    Capability = "simulation"
)

type Profile struct {
	OS           string              `json:"os"`
	Arch         string              `json:"arch"`
	Level        string              `json:"level"`
	Capabilities map[Capability]bool `json:"capabilities"`
	Readiness    string              `json:"readiness"`
}

func For(os, arch string) Profile {
	p := Profile{os, arch, "portable-only", map[Capability]bool{Simulation: true, DurableState: false, LinuxSandbox: false, NativeProcess: false}, "not checked; use doctor for a configured Linux mission"}
	if os == "linux" {
		p.Level = "native"
		p.Capabilities[DurableState] = true
		p.Capabilities[NativeProcess] = true
		p.Capabilities[LinuxSandbox] = true
	}
	return p
}
func Current() Profile { return For(runtime.GOOS, runtime.GOARCH) }
func (p Profile) Require(c Capability) error {
	if !p.Capabilities[c] {
		return fmt.Errorf("capability %s unavailable on %s/%s; no fallback", c, p.OS, p.Arch)
	}
	return nil
}
