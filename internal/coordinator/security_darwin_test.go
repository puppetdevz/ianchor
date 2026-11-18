//go:build darwin

package coordinator

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDirectorySecurity(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDirectorySecurity(dir, info); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/bin/chmod", "+a", "everyone allow list", dir).CombinedOutput(); err != nil {
		t.Fatalf("setting ACL: %v: %s", err, output)
	}
	if err := validateDirectorySecurity(dir, info); err == nil {
		t.Fatal("accepted directory with ACL")
	}
	if _, err := AcquireIn(dir); err == nil {
		t.Fatal("acquired in directory with ACL")
	}
}

func TestSecuritySeamDefaults(t *testing.T) {
	for name, pair := range map[string][2]any{
		"securityFstat":   {securityFstat, unix.Fstat},
		"securityFstatfs": {securityFstatfs, unix.Fstatfs},
		"securityQuery":   {securityQuery, queryAttributes},
	} {
		if reflect.ValueOf(pair[0]).Pointer() != reflect.ValueOf(pair[1]).Pointer() {
			t.Errorf("%s has wrong default", name)
		}
	}
}

func TestSecurityFailures(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDirectorySecurity(filepath.Join(dir, "missing"), info); err == nil {
		t.Fatal("missing path accepted")
	}
	other, err := os.Lstat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDirectorySecurity(dir, other); err == nil {
		t.Fatal("mismatched identity accepted")
	}
	if err := queryAttributes(-1, &unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT}, make([]byte, 32)); err == nil {
		t.Fatal("invalid descriptor accepted")
	}
	failure := errors.New("injected failure")
	for _, point := range []string{"fstat", "fstatfs", "remote", "volume query", "short volume", "no capability", "no validity", "attributes query", "short attributes", "unsupported attribute", "acl query", "short acl", "oversize acl", "no returned mask"} {
		t.Run(point, func(t *testing.T) {
			oldStat, oldFS, oldQuery := securityFstat, securityFstatfs, securityQuery
			t.Cleanup(func() { securityFstat, securityFstatfs, securityQuery = oldStat, oldFS, oldQuery })
			if point == "fstat" {
				securityFstat = func(int, *unix.Stat_t) error { return failure }
			}
			if point == "fstatfs" {
				securityFstatfs = func(int, *unix.Statfs_t) error { return failure }
			}
			if point == "remote" {
				securityFstatfs = func(_ int, st *unix.Statfs_t) error { *st = unix.Statfs_t{}; return nil }
			}
			securityQuery = func(_ int, attrs *unix.Attrlist, b []byte) error {
				if attrs.Volattr&unix.ATTR_VOL_ATTRIBUTES != 0 {
					if point == "attributes query" {
						return failure
					}
					binary.LittleEndian.PutUint32(b[:4], 44)
					binary.LittleEndian.PutUint32(b[4:8], unix.ATTR_CMN_EXTENDED_SECURITY)
					if point == "short attributes" {
						binary.LittleEndian.PutUint32(b[:4], 0)
					}
					if point == "unsupported attribute" {
						binary.LittleEndian.PutUint32(b[4:8], 0)
					}
					return nil
				}
				if attrs.Volattr != 0 {
					if point == "volume query" {
						return failure
					}
					binary.LittleEndian.PutUint32(b[:4], 36)
					binary.LittleEndian.PutUint32(b[8:12], 0x400)
					binary.LittleEndian.PutUint32(b[24:28], 0x400)
					switch point {
					case "short volume":
						binary.LittleEndian.PutUint32(b[:4], 0)
					case "no capability":
						binary.LittleEndian.PutUint32(b[8:12], 0)
					case "no validity":
						binary.LittleEndian.PutUint32(b[24:28], 0)
					}
					return nil
				}
				if point == "acl query" {
					return failure
				}
				binary.LittleEndian.PutUint32(b[:4], 24)
				binary.LittleEndian.PutUint32(b[4:8], unix.ATTR_CMN_RETURNED_ATTRS)
				switch point {
				case "short acl":
					binary.LittleEndian.PutUint32(b[:4], 0)
				case "oversize acl":
					binary.LittleEndian.PutUint32(b[:4], uint32(len(b)+1))
				case "no returned mask":
					binary.LittleEndian.PutUint32(b[4:8], 0)
				}
				return nil
			}
			if err := validateDirectorySecurity(dir, info); err == nil {
				t.Fatalf("%s was accepted", point)
			}
		})
	}
}
