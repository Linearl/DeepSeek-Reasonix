package session

// Task 1013: the durable checkpoint/operations publication into the removed
// bolt cache is gone. What remains of the publish seam is the work the callers
// still need done at each durability point.

func (s *Session) recoveryPublished(durable uint64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if durable+1 == s.next {
		s.durableRecent = detachMessages(s.recentMessages)
	}
	cut := 0
	for cut < len(s.commits) && s.commits[cut].LastSequence() <= durable {
		delete(s.operations, s.commits[cut].OperationID)
		cut++
	}
	if cut > 0 {
		s.commits = append([]Commit(nil), s.commits[cut:]...)
	}
	if s.externalHistory {
		s.projection.Messages = nil
	}
}
