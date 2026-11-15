//go:build !darwin && !linux

package anchor

import "errors"

func renameExclusive(from, to string) error {
	return errors.New("exclusive rename is not supported on this platform")
}

func renameAtExclusive(fromFD int, from string, toFD int, to string) error {
	return errors.New("exclusive rename is not supported on this platform")
}
