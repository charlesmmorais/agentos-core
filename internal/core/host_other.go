//go:build !linux && !darwin

package core

import (
	"errors"
	"os"
	"os/exec"
)

var errHostUnsupported = errors.New("host primitive unsupported; no unsafe fallback")

func lockFile(*os.File) error              { return errHostUnsupported }
func configureProcess(*exec.Cmd) error     { return errHostUnsupported }
func regularFile(string) (*os.File, error) { return nil, errHostUnsupported }
