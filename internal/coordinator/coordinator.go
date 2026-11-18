// Package coordinator serializes cooperating local iAnchor commands.
package coordinator

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

// Directory is the local per-user coordination directory, independent of HOME
// and the selected iCloud directory. It must not be deleted while in use.
func Directory() string {
	return filepath.Join("/Users/Shared", ".ianchor-"+strconv.Itoa(os.Geteuid()))
}

// Lease retains both the local flock and the SQLite write transaction.
type Lease struct {
	db     *sql.DB
	tx     *sql.Tx
	lockFD int
}

// These seams are the real OS/SQL calls in production; tests exercise errors
// that cannot be induced deterministically without a filesystem race.
var lstatPath = os.Lstat
var openFile = unix.Open
var closeFile = unix.Close
var statFile = unix.Fstat
var flockFile = unix.Flock
var chmodFile = unix.Fchmod
var chmodDirectory = chmodNewDirectory
var openDatabase = sql.Open

func chmodNewDirectory(path string) error {
	return unix.Fchmodat(unix.AT_FDCWD, path, 0o700, unix.AT_SYMLINK_NOFOLLOW)
}

// AcquireIn obtains a lease in a private directory. It never falls back to a
// different location when validation or lock acquisition fails.
func AcquireIn(dir string) (_ *Lease, retErr error) {
	if err := os.Mkdir(dir, 0o700); err == nil {
		// Only normalize a newly created directory, never an existing one.
		// NOFOLLOW prevents changing a symlink target if the path is replaced.
		if err := chmodDirectory(dir); err != nil {
			return nil, err
		}
	} else if !os.IsExist(err) {
		return nil, err
	}
	info, err := lstatPath(dir)
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0o700 || st.Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("unsafe coordinator directory: %s", dir)
	}
	if err := validateDirectorySecurity(dir, info); err != nil {
		return nil, err
	}
	// SQLite's POSIX record lock can be released by closing *any* descriptor
	// for its inode in this process. An independent flock remains held when
	// unrelated descriptors are closed, so keep it until the whole operation
	// and the SQLite transaction have finished.
	lockPath := filepath.Join(dir, "coordinator.lock")
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	lockFD, err := openFile(lockPath, flags|unix.O_CREAT|unix.O_EXCL, 0o600)
	created := err == nil
	if os.IsExist(err) {
		lockFD, err = openFile(lockPath, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, closeFile(lockFD))
		}
	}()
	if created {
		if err := chmodFile(lockFD, 0o600); err != nil {
			return nil, err
		}
	}
	var lockStat unix.Stat_t
	if err := statFile(lockFD, &lockStat); err != nil {
		return nil, err
	}
	if lockStat.Mode&unix.S_IFMT != unix.S_IFREG || lockStat.Mode&0o7777 != 0o600 || lockStat.Nlink != 1 || lockStat.Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("unsafe coordinator lock: %s", lockPath)
	}
	if err := flockFile(lockFD, unix.LOCK_EX); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "coordinator.db")
	fd, err := openFile(path, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err == nil {
		chmodErr := chmodFile(fd, 0o600)
		closeErr := closeFile(fd)
		if err := errors.Join(chmodErr, closeErr); err != nil {
			return nil, err
		}
	} else if !os.IsExist(err) {
		return nil, err
	}
	fileInfo, err := lstatPath(path)
	if err != nil {
		return nil, err
	}
	fileStat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok || !fileInfo.Mode().IsRegular() || fileStat.Nlink != 1 || fileStat.Uid != uint32(os.Geteuid()) || fileInfo.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("unsafe coordinator database: %s", path)
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		sidecar, err := lstatPath(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sideStat, ok := sidecar.Sys().(*syscall.Stat_t)
		if !ok || !sidecar.Mode().IsRegular() || sidecar.Mode().Perm() != 0o600 || sideStat.Uid != uint32(os.Geteuid()) || sideStat.Nlink != 1 {
			return nil, fmt.Errorf("unsafe coordinator sidecar: %s", path+suffix)
		}
	}
	u := &url.URL{Scheme: "file", Path: path}
	db, err := openDatabase("sqlite", u.String()+"?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Lease{db: db, tx: tx, lockFD: lockFD}, nil
}

// Close releases the lease even if rolling back the transaction fails.
func (l *Lease) Close() error {
	return errors.Join(l.tx.Rollback(), l.db.Close(), closeFile(l.lockFD))
}
