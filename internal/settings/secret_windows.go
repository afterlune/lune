//go:build windows

package settings

import (
	"errors"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func protectSecret(secret string) ([]byte, error) {
	if secret == "" {
		return nil, errors.New("secret is empty")
	}
	input := []byte(secret)
	inBlob := windows.DataBlob{Size: uint32(len(input)), Data: &input[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&inBlob, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func unprotectSecret(encrypted []byte) (string, error) {
	if len(encrypted) == 0 {
		return "", errors.New("encrypted secret is empty")
	}
	inBlob := windows.DataBlob{Size: uint32(len(encrypted)), Data: &encrypted[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&inBlob, nil, nil, 0, nil, 0, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	return strings.TrimRight(string(unsafe.Slice(out.Data, out.Size)), "\x00"), nil
}
