//go:build darwin

package coordinator

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

var securityFstat = unix.Fstat
var securityFstatfs = unix.Fstatfs
var securityQuery = queryAttributes

// validateDirectorySecurity verifies that mode 0700 is not weakened by a
// Darwin ACL and that SQLite locks are on a local, ACL-capable filesystem.
// It opens the directory, never the SQLite inode (closing an unrelated SQLite
// descriptor would release this process's POSIX record locks).
func validateDirectorySecurity(path string, expected os.FileInfo) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := securityFstat(fd, &stat); err != nil {
		return err
	}
	original, ok := expected.Sys().(*syscall.Stat_t)
	if !ok || original.Dev != stat.Dev || original.Ino != stat.Ino || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o777 != 0o700 {
		return fmt.Errorf("coordinator directory changed: %s", path)
	}
	var fs unix.Statfs_t
	if err := securityFstatfs(fd, &fs); err != nil {
		return err
	}
	if fs.Flags&unix.MNT_LOCAL == 0 {
		return fmt.Errorf("coordinator filesystem is not local: %s", path)
	}
	caps := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Volattr: unix.ATTR_VOL_INFO | unix.ATTR_VOL_CAPABILITIES}
	var volume [36]byte
	if err := securityQuery(fd, &caps, volume[:]); err != nil {
		return err
	}
	const extendedSecurity = 0x400
	if binary.LittleEndian.Uint32(volume[:4]) < uint32(len(volume)) || binary.LittleEndian.Uint32(volume[8:12])&extendedSecurity == 0 || binary.LittleEndian.Uint32(volume[24:28])&extendedSecurity == 0 {
		return fmt.Errorf("coordinator filesystem does not report ACL support: %s", path)
	}
	volumeAttrs := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Volattr: unix.ATTR_VOL_INFO | unix.ATTR_VOL_ATTRIBUTES}
	var supported [44]byte
	if err := securityQuery(fd, &volumeAttrs, supported[:]); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(supported[:4]) < uint32(len(supported)) || binary.LittleEndian.Uint32(supported[4:8])&unix.ATTR_CMN_EXTENDED_SECURITY == 0 {
		return fmt.Errorf("coordinator filesystem does not report security attributes: %s", path)
	}
	attrs := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_EXTENDED_SECURITY}
	var data [8192]byte
	if err := securityQuery(fd, &attrs, data[:]); err != nil {
		return err
	}
	size := binary.LittleEndian.Uint32(data[:4])
	if size < 24 || size > uint32(len(data)) || binary.LittleEndian.Uint32(data[4:8])&unix.ATTR_CMN_RETURNED_ATTRS == 0 {
		return fmt.Errorf("invalid coordinator security attributes: %s", path)
	}
	if binary.LittleEndian.Uint32(data[4:8])&unix.ATTR_CMN_EXTENDED_SECURITY != 0 {
		return fmt.Errorf("coordinator directory has an ACL: %s", path)
	}
	return nil
}

func queryAttributes(fd int, attrs *unix.Attrlist, data []byte) error {
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(attrs)), uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), unix.FSOPT_REPORT_FULLSIZE, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
