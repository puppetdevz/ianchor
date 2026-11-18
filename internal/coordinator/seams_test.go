package coordinator

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSeamDefaults(t *testing.T) {
	for name, pair := range map[string][2]any{
		"lstatPath":      {lstatPath, os.Lstat},
		"openFile":       {openFile, unix.Open},
		"closeFile":      {closeFile, unix.Close},
		"statFile":       {statFile, unix.Fstat},
		"flockFile":      {flockFile, unix.Flock},
		"chmodFile":      {chmodFile, unix.Fchmod},
		"chmodDirectory": {chmodDirectory, chmodNewDirectory},
		"openDatabase":   {openDatabase, sql.Open},
	} {
		if reflect.ValueOf(pair[0]).Pointer() != reflect.ValueOf(pair[1]).Pointer() {
			t.Errorf("%s is not its real default", name)
		}
	}
}

func TestAcquireFailures(t *testing.T) {
	failure := errors.New("injected failure")
	for _, point := range []string{"directory stat", "open", "close", "file stat", "sidecar stat", "database open", "lock stat", "lock unsafe", "lock flock", "database file open", "directory chmod", "lock chmod", "database chmod"} {
		t.Run(point, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "private")
			oldLstat, oldOpen, oldClose, oldDB, oldStat, oldFlock, oldChmod, oldDirChmod := lstatPath, openFile, closeFile, openDatabase, statFile, flockFile, chmodFile, chmodDirectory
			t.Cleanup(func() {
				lstatPath, openFile, closeFile, openDatabase, statFile, flockFile, chmodFile, chmodDirectory = oldLstat, oldOpen, oldClose, oldDB, oldStat, oldFlock, oldChmod, oldDirChmod
			})
			lstatPath = func(path string) (os.FileInfo, error) {
				if (point == "directory stat" && path == dir) || (point == "file stat" && path == filepath.Join(dir, "coordinator.db")) || (point == "sidecar stat" && strings.HasSuffix(path, "-journal")) {
					return nil, failure
				}
				return os.Lstat(path)
			}
			if point == "open" {
				openFile = func(string, int, uint32) (int, error) { return -1, failure }
			}
			if point == "database file open" {
				openFile = func(path string, flags int, mode uint32) (int, error) {
					if strings.HasSuffix(path, "coordinator.db") {
						return -1, failure
					}
					return unix.Open(path, flags, mode)
				}
			}
			if point == "close" {
				closeFile = func(fd int) error { _ = unix.Close(fd); return failure }
			}
			if point == "lock stat" {
				statFile = func(int, *unix.Stat_t) error { return failure }
			}
			if point == "lock unsafe" {
				statFile = func(fd int, stat *unix.Stat_t) error {
					if err := unix.Fstat(fd, stat); err != nil {
						return err
					}
					stat.Nlink = 2
					return nil
				}
			}
			if point == "lock flock" {
				flockFile = func(int, int) error { return failure }
			}
			if point == "directory chmod" {
				chmodDirectory = func(string) error { return failure }
			}
			if point == "lock chmod" || point == "database chmod" {
				calls := 0
				chmodFile = func(fd int, mode uint32) error {
					calls++
					if (point == "lock chmod" && calls == 1) || (point == "database chmod" && calls == 2) {
						return failure
					}
					return unix.Fchmod(fd, mode)
				}
			}
			if point == "database open" {
				openDatabase = func(string, string) (*sql.DB, error) { return nil, failure }
			}
			if _, err := AcquireIn(dir); point == "lock unsafe" {
				if err == nil || !strings.Contains(err.Error(), "unsafe coordinator lock") {
					t.Fatalf("got %v", err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("got %v, want injected failure", err)
			}
		})
	}
}
