package secure

import (
	"encoding/base64"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

const Prefix = "dpapi:"

func Protect(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	raw := []byte(value)
	in := windows.DataBlob{Size: uint32(len(raw)), Data: &raw[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	data := unsafe.Slice(out.Data, int(out.Size))
	return Prefix + base64.StdEncoding.EncodeToString(data), nil
}

func Unprotect(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) < len(Prefix) || value[:len(Prefix)] != Prefix {
		return "", errors.New("value is not protected")
	}
	raw, err := base64.StdEncoding.DecodeString(value[len(Prefix):])
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", errors.New("protected value is empty")
	}
	in := windows.DataBlob{Size: uint32(len(raw)), Data: &raw[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return string(unsafe.Slice(out.Data, int(out.Size))), nil
}
