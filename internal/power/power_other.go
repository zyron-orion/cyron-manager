//go:build !windows

package power

import "errors"

func Shutdown(delaySeconds int) error {
	return errors.New("shutdown is only supported on Windows")
}
