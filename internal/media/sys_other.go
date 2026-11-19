//go:build !windows

package media

import "os/exec"

func hideConsole(cmd *exec.Cmd) {}
