package session

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"reasonix/internal/provider"
)

// messageIdentities is every stable message id the log holds live. The writer
// needs it because an external-history projection drops durable message
// bodies at open and after every recovery checkpoint, so the projection alone
// cannot see an id claimed before its last checkpoint (task 398, upstream
// #11006 → #10893).
type messageIdentities map[string]struct{}

func identitiesOf(ids []string) messageIdentities {
	set := make(messageIdentities, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func (ids messageIdentities) holds(id string) bool {
	_, ok := ids[id]
	return ok
}

func (ids messageIdentities) list() []string {
	return slices.Collect(maps.Keys(ids))
}

// identityChange is one commit's effect on the live set, kept apart so a
// refused commit leaves the set untouched.
type identityChange struct {
	reset bool
	live  map[string]bool
	// duplicate is the first message/complete id the commit repeats.
	duplicate string
}

func (c *identityChange) has(ids messageIdentities, id string) bool {
	if live, ok := c.live[id]; ok {
		return live
	}
	if c.reset {
		return false
	}
	_, ok := ids[id]
	return ok
}

// changeFor mirrors the projection's message semantics: a complete or upsert
// claims an id, a history/replace or legacy/import resets the live set to the
// replacement's ids. A payload it cannot decode is left for the projection to
// refuse. The fork's event vocabulary has no message/retract.
func (ids messageIdentities) changeFor(commit Commit) identityChange {
	change := identityChange{live: map[string]bool{}}
	for _, event := range commit.Events {
		switch event.Kind {
		case "message/complete", "message/upsert":
			id := eventMessageID(event.Payload)
			if id == "" {
				continue
			}
			if event.Kind == "message/complete" && change.has(ids, id) {
				if change.duplicate == "" {
					change.duplicate = id
				}
				continue
			}
			change.live[id] = true
		case "history/replace", "legacy/import":
			messages, err := replacementEventMessages(event.Payload)
			if err != nil {
				continue
			}
			change.reset, change.live = true, map[string]bool{}
			for _, message := range messages {
				if message.ID != "" {
					change.live[message.ID] = true
				}
			}
		}
	}
	return change
}

func (ids *messageIdentities) apply(change identityChange) {
	if change.reset || *ids == nil {
		*ids = messageIdentities{}
	}
	for id, live := range change.live {
		if live {
			(*ids)[id] = struct{}{}
		} else {
			delete(*ids, id)
		}
	}
}

// admit records a commit a reader replays. A repeated message/complete keeps
// the id's first occurrence, exactly as the projection does, so admitting a
// damaged log converges on the same live set the readers expose.
func (ids *messageIdentities) admit(commit Commit) {
	ids.apply(ids.changeFor(commit))
}

// eventMessageID pulls the stable id out of a message/complete or
// message/upsert payload. Payloads still held as references decode to "" and
// are skipped — every admission site resolves payloads first.
func eventMessageID(payload json.RawMessage) string {
	var body struct {
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return ""
	}
	return body.Message.ID
}

// replacementEventMessages decodes the messages array of a history/replace or
// legacy/import payload. Unknown fields (reason, goal, modelRef…) are ignored
// on purpose: this helper only feeds the identity set, and the projection
// remains the canonical validator.
func replacementEventMessages(payload json.RawMessage) ([]provider.Message, error) {
	var body struct {
		Messages []provider.Message `json:"messages"`
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("session: empty replacement payload")
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, err
	}
	if body.Messages == nil {
		return nil, fmt.Errorf("session: replacement payload has no messages")
	}
	return body.Messages, nil
}

func duplicateMessageError(id string) error {
	return fmt.Errorf("%w: message/complete %q", ErrDuplicateMessageID, id)
}
