package kernel

import (
	"testing"
	"time"
)

func TestPortablePaths(t *testing.T) {
	for _, p := range []string{"/data", `C:\data`, `D:/data`, `\\server\share\file`} {
		if !AbsolutePath(p) {
			t.Errorf("reject %q", p)
		}
	}
	for _, p := range []string{"data", `C:data`, `\data`, `\\server\`, "/a\x00b"} {
		if AbsolutePath(p) {
			t.Errorf("accept %q", p)
		}
	}
}
func TestTransitions(t *testing.T) {
	for _, terminal := range []string{"completed", "cancelled", "invalid"} {
		for _, action := range []string{"resume", "pause", "cancel"} {
			if _, err := Transition(terminal, action); err == nil {
				t.Fatal("revived terminal state")
			}
		}
	}
	if s, e := Transition("paused", "resume"); e != nil || s != "active" {
		t.Fatal(s, e)
	}
	if _, e := Transition("active", "approve"); e == nil {
		t.Fatal("unknown command accepted")
	}
	now := time.Unix(100, 0)
	if !Due("active", now, now) || Due("paused", now, now) || Due("active", now.Add(time.Second), now) {
		t.Fatal("schedule boundary")
	}
}
func TestStrictProtocol(t *testing.T) {
	valid := `{"mission":"test","workspace":"/data","script":"/script.py","interval_seconds":1,"timeout_seconds":1,"max_cycles":2}`
	p, err := DecodeProtocol([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Simulate(p)
	if err != nil || result["execution"] != false || result["persistence"] != false {
		t.Fatal(result, err)
	}
	for _, raw := range []string{valid + valid, `{"unknown":1}`, `null`, string(make([]byte, 65537))} {
		if _, err := DecodeProtocol([]byte(raw)); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
	p.MaxCycles = 10001
	if _, err := Simulate(p); err == nil {
		t.Fatal("ignored budget")
	}
}
