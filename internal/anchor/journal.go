// Package anchor implements the ianchor operations: init, add, recover, relink,
// remove and list. Every mutating operation records compensating steps so an
// ordinary error leaves the filesystem and database in their previous state.
package anchor

import (
	"errors"
	"fmt"
	"strings"
)

// committedCleanupError reports that the requested operation committed, but
// displaced data could not be removed. The path must remain visible for manual recovery.
type committedCleanupError struct {
	operation string
	path      string
	err       error
}

func (e *committedCleanupError) Error() string {
	return fmt.Sprintf("%s completed but cleanup failed; %s may contain only partial data: %v", e.operation, e.path, e.err)
}
func (e *committedCleanupError) Unwrap() error { return e.err }

// cleanupLocationError carries the actual quarantine location after a cleanup
// moved the object. An empty path means the pinned directory became detached.
type cleanupLocationError struct {
	path string
	err  error
}

func (e *cleanupLocationError) Error() string { return e.err.Error() }
func (e *cleanupLocationError) Unwrap() error { return e.err }
func cleanupLocation(err error, fallback string) string {
	var located *cleanupLocationError
	if errors.As(err, &located) {
		if located.path == "" {
			return "location unknown (backup directory detached)"
		}
		return located.path
	}
	return fallback
}

type undoStep struct {
	description string
	undo        func() error
}

type journal struct {
	steps []undoStep
}

func (j *journal) add(description string, undo func() error) {
	j.steps = append(j.steps, undoStep{description: description, undo: undo})
}

// commit forgets the compensations because every step succeeded.
func (j *journal) commit() { j.steps = nil }

// rollback runs compensations in reverse order and reports the ones that could
// not be completed so the caller can tell the user what state the disk is in.
func (j *journal) rollback() []string {
	var failures []string
	for i := len(j.steps) - 1; i >= 0; i-- {
		if err := j.steps[i].undo(); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", j.steps[i].description, err))
			for k := i - 1; k >= 0; k-- {
				failures = append(failures, "not attempted: "+j.steps[k].description)
			}
			break
		}
	}
	j.steps = nil
	return failures
}

// fail wraps err with any compensation failures.
func (j *journal) fail(err error) error {
	if failures := j.rollback(); len(failures) > 0 {
		return fmt.Errorf("%w (rollback failed: %s)", err, strings.Join(failures, "; "))
	}
	return err
}

// errorf mirrors the message style of the Node implementation: values are
// wrapped in double quotes without escaping.
func errorf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
