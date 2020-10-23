// Package testing includes useful mocks for testing initial
// sync status in unit tests.
package testing

import "context"

// Sync defines a mock for the sync service.
type Sync struct {
	IsSyncing bool
}

// Syncing --
func (s *Sync) Syncing() bool {
	return s.IsSyncing
}

// Status --
func (s *Sync) Status() error {
	return nil
}

// Resync --
func (s *Sync) Resync(ctx context.Context) error {
	return nil
}
