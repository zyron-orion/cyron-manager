//go:build !windows

package disk

func Usage(path string) (total int64, free int64, err error) {
	return 0, 0, nil
}
