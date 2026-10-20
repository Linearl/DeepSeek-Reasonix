package main

import (
	"reasonix/internal/control"
)

// UnfiledInputView is one checkpoint-recovered prompt that never entered the
// transcript (task 1023 phase 0). Strictly read-only: the desktop only reads
// the reconciliation result and renders it; marking an entry handled is
// frontend-local state and never writes the transcript or the event log.
type UnfiledInputView struct {
	Turn     int    `json:"turn"`
	MsgIndex int    `json:"msgIndex"`
	Time     int64  `json:"time"` // unix milliseconds; 0 when the checkpoint predates timestamps
	Prompt   string `json:"prompt"`
	// AnchorIndex is the transcript position the input belongs at (the
	// checkpoint's msgIndex — recorded before the user message is appended).
	AnchorIndex int `json:"anchorIndex"`
	// AnchorMessageId is the ID of the message currently at AnchorIndex, when
	// in range; the transcript inserts the recovered card before that message.
	AnchorMessageId string `json:"anchorMessageId,omitempty"`
}

// UnfiledInputs reconciles the active tab's checkpoints against its transcript.
func (a *App) UnfiledInputs() []UnfiledInputView {
	return a.UnfiledInputsForTab("")
}

// UnfiledInputsForTab reconciles the requested tab. Session load, tab switch
// and startup call this through the frontend so recovered inputs surface
// without user action.
func (a *App) UnfiledInputsForTab(tabID string) []UnfiledInputView {
	a.mu.RLock()
	var ctrl control.SessionAPI
	if tab := a.tabByIDLocked(tabID); tab != nil {
		ctrl = tab.Ctrl
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return []UnfiledInputView{}
	}
	inputs := ctrl.UnfiledInputs()
	out := make([]UnfiledInputView, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, UnfiledInputView{
			Turn:            in.Turn,
			MsgIndex:        in.MsgIndex,
			Time:            in.Time.UnixMilli(),
			Prompt:          in.Prompt,
			AnchorIndex:     in.AnchorIndex,
			AnchorMessageId: in.AnchorID,
		})
	}
	return out
}
