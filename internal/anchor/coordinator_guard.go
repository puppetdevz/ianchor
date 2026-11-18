package anchor

import (
	"fmt"
	"os"

	"github.com/puppetdevz/ianchor/internal/coordinator"
)

// coordinatorDirectory is the per-user lock location shared by CLI commands.
var coordinatorDirectory = coordinator.Directory

// ValidateCoordinatorConfig rejects a config tree that could contain or
// detach the local operation lock.
func ValidateCoordinatorConfig(path string) error {
	return validateCoordinatorPath(path, true)
}

// validateCoordinatorPath refuses mutations that could detach the lock
// directory from the pathname through which other commands find it. A final
// symlink is not followed when the operation only mutates the link itself.
func validateCoordinatorPath(path string, followFinal bool) error {
	dir := coordinatorDirectory()
	if _, err := statPath(dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if followFinal {
		resolved, err := evalSymlinksPath(path)
		if err == nil {
			path = resolved
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	overlap, err := identityOverlap(dir, path, followFinal)
	if err != nil {
		return err
	}
	if overlap {
		return fmt.Errorf("Path %q overlaps the local coordination directory", path)
	}
	return nil
}
