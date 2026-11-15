//go:build darwin

package anchor

import "golang.org/x/sys/unix"

// renameExclusive never replaces an entry which appeared after validation.
func renameExclusive(from, to string) error {
	return unix.RenamexNp(from, to, unix.RENAME_EXCL)
}

func renameAtExclusive(fromFD int, from string, toFD int, to string) error {
	return unix.RenameatxNp(fromFD, from, toFD, to, unix.RENAME_EXCL)
}
