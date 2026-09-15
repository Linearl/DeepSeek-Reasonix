package session

import (
	"context"
	"errors"
)

// CloseAll releases every unbound runtime owned by this service. Long-lived
// hosts normally rely on idle retention; process shutdown and isolated tests
// use this explicit boundary to release writer leases deterministically.
func (s *Service) CloseAll(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	runtimes := make([]*Runtime, 0, len(s.active))
	for _, runtime := range s.active {
		runtimes = append(runtimes, runtime)
	}
	s.mu.Unlock()
	var closeErr error
	for _, runtime := range runtimes {
		closeErr = errors.Join(closeErr, s.closeOwned(ctx, runtime, ""))
	}
	return closeErr
}
