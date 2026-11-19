//go:build !windows

package aria2

import "os/exec"

func hideConsole(cmd *exec.Cmd) {}
