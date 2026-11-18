//go:build windows

package hostservice

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// protect encrypts data with DPAPI bound to the current Windows user. A
// secret copied to another account or machine cannot be read.
func protect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return []byte{}, nil
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	result := make([]byte, out.Size)
	copy(result, unsafe.Slice(out.Data, out.Size))
	return result, nil
}

// unprotect reverses protect.
func unprotect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty secret store")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	result := make([]byte, out.Size)
	copy(result, unsafe.Slice(out.Data, out.Size))
	return result, nil
}
