//go:build windows

package disk

import (
	"syscall"
	"unsafe"
)

type usage struct {
	Total uint64
	Free  uint64
}

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceExW = kernel32.NewProc("GetDiskFreeSpaceExW")
)

func Usage(path string) (total int64, free int64, err error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var freeToCaller, totalBytes, freeBytes uint64
	r1, _, e := getDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&freeBytes)),
	)
	if r1 == 0 {
		return 0, 0, e
	}
	return int64(totalBytes), int64(freeBytes), nil
}
