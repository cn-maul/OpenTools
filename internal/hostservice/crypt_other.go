//go:build !windows

package hostservice

import "errors"

// protect is a pass-through on platforms without a user-bound keystore.
// The file is still restricted to the current user by permissions alone, so
// tools must treat stored secrets as readable by the same user.
func protect(data []byte) ([]byte, error) {
	return data, nil
}

func unprotect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty secret store")
	}
	return data, nil
}
