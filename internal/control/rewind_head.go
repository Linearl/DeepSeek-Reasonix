package control

import ()

// undoHeadRewind returns the controller to the head it left through an
// in-place conversation rewind, provided nothing was added on the rewind head
// since: the empty rewind head is retired and its parent becomes current. A
// rewind head that was continued stays a version and undo leaves it alone.
// undoHeadRewind undid an in-place conversation rewind by returning to the
// parent head of the empty rewind head. tl1280: the schema-2 head family left
// with the write-path slow lane, so there is no head to return to — the undo
// is a no-op and the conversation half of UndoRewind is simply not claimed
// (files still undo normally).
func (c *Controller) undoHeadRewind() bool {
	return false
}
