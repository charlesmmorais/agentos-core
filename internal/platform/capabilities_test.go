package platform

import "testing"

func TestFailClosed(t *testing.T) {
	for _, os := range []string{"windows", "darwin", "js", "wasip1", "unknown"} {
		p := For(os, "test")
		if p.Require(Simulation) != nil || p.Require(DurableState) == nil || p.Require(LinuxSandbox) == nil || p.Require(NativeProcess) == nil {
			t.Fatal(p)
		}
	}
	p := For("linux", "amd64")
	if p.Require(DurableState) != nil || p.Require(LinuxSandbox) != nil || p.Require("unknown") == nil {
		t.Fatal(p)
	}
}
