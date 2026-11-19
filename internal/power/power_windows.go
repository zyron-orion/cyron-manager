//go:build windows

package power

import (
	"os/exec"
	"strconv"
)

func Shutdown(delaySeconds int) error {
	if delaySeconds < 0 {
		delaySeconds = 0
	}
	return exec.Command("shutdown", "/s", "/t", strconv.Itoa(delaySeconds)).Start()
}
