//go:build !darwin

package coordinator

import (
	"errors"
	"os"
)

// The product supports Darwin; do not silently accept an unverified ACL or
// filesystem-locking implementation on other platforms.
func validateDirectorySecurity(string, os.FileInfo) error {
	return errors.New("local coordinator is unsupported on this platform")
}
