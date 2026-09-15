package main

import (
	"reasonix/internal/tool/builtin"
)

// FeedbackEntryView is the desktop wire shape for one local inbox row (task 121).
type FeedbackEntryView struct {
	At      string   `json:"at"`
	Kind    string   `json:"kind"`
	Text    string   `json:"text"`
	Tags    []string `json:"tags,omitempty"`
	Session string   `json:"session,omitempty"`
	Model   string   `json:"model,omitempty"`
}

// ListFeedbackEntries returns the local feedback inbox, newest last.
func (a *App) ListFeedbackEntries(limit int) ([]FeedbackEntryView, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	entries, err := builtin.ListFeedbackEntries(limit)
	if err != nil {
		return nil, err
	}
	out := make([]FeedbackEntryView, 0, len(entries))
	for _, e := range entries {
		out = append(out, FeedbackEntryView{
			At: e.At, Kind: e.Kind, Text: e.Text, Tags: e.Tags, Session: e.Session, Model: e.Model,
		})
	}
	return out, nil
}

// ClearFeedbackEntries empties the local feedback inbox.
func (a *App) ClearFeedbackEntries() error {
	return builtin.ClearFeedbackEntries()
}
