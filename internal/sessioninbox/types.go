package sessioninbox

import (
	"errors"
	"time"
)

// SchemaVersion is the on-disk format version. Unknown higher versions load
// read-only and force pause; they never auto-execute.
const SchemaVersion = 3

// Default capacity limits. 任务999: user decision 2026-10-19 — steer injection
// and a normal turn cost the same request, so blocking large messages until
// the next turn bought nothing; the single-item ceiling 4→16 MiB and the total
// 64→128 MiB.
const (
	DefaultMaxItems      = 64
	DefaultMaxItemBytes  = 16 << 20  // 16 MiB (任务999: was 4 MiB)
	DefaultMaxTotalBytes = 128 << 20 // 128 MiB (任务999: was 64 MiB)
	DefaultPreviewRunes  = 120

	// SoftNoticeItemBytes (任务999) is the advisory threshold, NOT a gate: an
	// item whose encoded blob exceeds it still enqueues (the hard ceiling
	// stays DefaultMaxItemBytes) but earns one UI-layer notice — many images
	// inflate transfer cost and can slow the response. Same 任务894 tier as
	// the silent delivery receipts: no action needed → the model never reads
	// it, the human sees a transcript notice.
	SoftNoticeItemBytes = 4 << 20 // 4 MiB
)

// InboxIntent distinguishes durable follow-up turns from mid-turn steers.
type InboxIntent string

const (
	IntentFollowup InboxIntent = "followup"
	IntentSteer    InboxIntent = "steer"
)

// InboxState is the durable lifecycle of one queue item.
type InboxState string

const (
	StateQueued        InboxState = "queued"
	StateSteerAccepted InboxState = "steer_accepted"
	StateSteerConsumed InboxState = "steer_consumed"
	StateRunning       InboxState = "running"
	StateBlocked       InboxState = "blocked"
	StateUncertain     InboxState = "uncertain"
)

// BlockReason values that park a row for user review instead of marking it
// applied residue. Recovery's settled drop (task 263/641) keeps these rows:
// their content did not durably reach the transcript, so a settled mail
// delivery is not evidence the instruction was applied. Any other Uncertain
// row whose source mail was delivered settles as residue.
const (
	// BlockReasonSteerUnapplied parks an accepted steer that left the agent
	// queue without being injected (Controller.onInboxUnappliedSteer).
	BlockReasonSteerUnapplied = "steer accepted but unapplied before turn exit"
	// BlockReasonTranscriptNotDurable parks items whose turn completed but the
	// transcript snapshot did not become durable (task 585's keep half —
	// nothing silently drops when the application receipt itself is lost).
	BlockReasonTranscriptNotDurable = "turn completed but transcript snapshot failed"
	// BlockReasonRestartOrphan parks an in-flight item whose owning turn was
	// killed mid-flight (crash or hard restart). 任务840: constant-ified from
	// the recovery literal — the only writer stays
	// RecoverOrphanedInFlightOwnedBy, so the reason itself is the proof the
	// row was admitted into a turn of a PREVIOUS process (the planned-restart
	// settle reads it as that admission evidence).
	BlockReasonRestartOrphan = "in-flight owner is no longer active"
)

// Disposition reports how an admission attempt settled.
type Disposition string

const (
	DispositionStarted          Disposition = "started"
	DispositionSteerAccepted    Disposition = "steer_accepted"
	DispositionQueuedFollowup   Disposition = "queued_followup"
	DispositionRejectedBusy     Disposition = "rejected_busy"
	DispositionRejectedRotating Disposition = "rejected_rotating"
	DispositionRejectedClosed   Disposition = "rejected_closed"
	DispositionRejectedCapacity Disposition = "rejected_capacity"
	DispositionIdempotentHit    Disposition = "idempotent_hit"
)

// Sentinel errors for capacity and validation.
var (
	ErrCapacityItems       = errors.New("session inbox item limit reached")
	ErrCapacityBytes       = errors.New("session inbox byte limit reached")
	ErrItemTooLarge        = errors.New("inbox item exceeds single-item size limit")
	ErrNotFound            = errors.New("inbox item not found")
	ErrInvalidState        = errors.New("inbox item state does not allow this operation")
	ErrSchemaReadonly      = errors.New("inbox schema is newer and is read-only")
	ErrClosed              = errors.New("inbox is closed")
	ErrSnapshotBusy        = errors.New("inbox snapshot is busy")
	ErrEmpty               = errors.New("inbox item body is empty")
	ErrPaused              = errors.New("inbox is paused")
	ErrIdempotencyConflict = errors.New("idempotency key was already used for different input")
)

// InboxItemMeta is the durable metadata kept in the manifest (never the body).
type InboxItemMeta struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionId,omitempty"`
	Intent    InboxIntent `json:"intent"`
	State     InboxState  `json:"state"`
	Revision  int64       `json:"revision"`
	// BlobName is the on-disk blob filename stem (without .json). Empty means
	// legacy layout where the blob is named by item ID. Updates write a new
	// immutable blob and switch this pointer after manifest commit.
	BlobName    string       `json:"blobName,omitempty"`
	Source      string       `json:"source,omitempty"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	Preview     string       `json:"preview"`
	ByteSize    int64        `json:"byteSize"`
	Checksum    string       `json:"checksum"`
	Idempotency string       `json:"idempotencyKey,omitempty"`
	Refs        []RefSummary `json:"refs,omitempty"`
	BlockReason string       `json:"blockReason,omitempty"`
	RunID       string       `json:"runId,omitempty"`
	// Task 309 read-receipt bookkeeping. ReceiptRequested mirrors the
	// envelope flag; CollabMsgID/CollabMailTo carry the original mail's ID and
	// recipient so the consume-side receipt knows who to answer and which
	// mail it is answering — both survive merges because they live on the
	// delivery record, not on queue rows.
	ReceiptRequested bool   `json:"receiptRequested,omitempty"`
	CollabMsgID      string `json:"collabMsgId,omitempty"`
	CollabMailTo     string `json:"collabMailTo,omitempty"`
	// FoldedReceipts records original mails whose rows were folded into this
	// one by a merge (task 309 × 221): consuming the merged body receipts
	// every folded sender, not just the first mail.
	FoldedReceipts []ReceiptRef `json:"foldedReceipts,omitempty"`
}

// ReceiptRef is one original mail folded into a surviving item by a merge.
type ReceiptRef struct {
	MsgID  string `json:"msgId,omitempty"`
	MailTo string `json:"mailTo,omitempty"`
}

// RefSummary is a short reference summary stored in the manifest.
type RefSummary struct {
	Kind    string `json:"kind"` // clean_git | frozen | external | attachment
	Path    string `json:"path,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Preview string `json:"preview,omitempty"`
}

// RefSnapshot freezes a resolved @-reference at enqueue time.
type RefSnapshot struct {
	Kind         string `json:"kind"` // clean_git | frozen | external | attachment | mcp
	Path         string `json:"path,omitempty"`
	DisplayPath  string `json:"displayPath,omitempty"`
	RepoIdentity string `json:"repoIdentity,omitempty"`
	Commit       string `json:"commit,omitempty"`
	RangeStart   int    `json:"rangeStart,omitempty"`
	RangeEnd     int    `json:"rangeEnd,omitempty"`
	Content      []byte `json:"content,omitempty"`
	ContentSHA   string `json:"contentSha,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
	Server       string `json:"server,omitempty"`
	URI          string `json:"uri,omitempty"`
}

// StructuredInvocation is a frozen slash/command invocation.
type StructuredInvocation struct {
	Name    string            `json:"name,omitempty"`
	Kind    string            `json:"kind,omitempty"`
	Offset  int               `json:"offset,omitempty"`
	Args    map[string]string `json:"args,omitempty"`
	Display string            `json:"display,omitempty"`
}

// PromptEnvelope is the full durable body stored only in blobs/<id>.json.
type PromptEnvelope struct {
	FingerprintVersion int    `json:"fingerprintVersion,omitempty"`
	RequestFingerprint string `json:"requestFingerprint,omitempty"`
	DisplayText        string `json:"displayText"`
	RawText            string `json:"rawText"`
	SubmitText         string `json:"submitText"`
	// Invocation is retained for schema-v1 compatibility. New writers use
	// Invocations so multiple rich-composer entities preserve visual order.
	// ReceiptRequested (task 309) rides the envelope so the read receipt
	// survives blob round-trips: the recipient sends a system receipt back to
	// the mail's sender when this item is consumed into context (steer
	// injection or drain). Delivery-level confirmation needs no flag — the
	// talk_to_session return value already covers it.
	ReceiptRequested bool `json:"receiptRequested,omitempty"`
	// CollabMsgID/CollabMailTo (task 309) carry the original mail's ID and
	// recipient so the consume-side receipt knows which mail it is answering
	// and who originally asked for it; both survive merges because they ride
	// the delivery record, not queue rows.
	CollabMsgID  string                 `json:"collabMsgId,omitempty"`
	CollabMailTo string                 `json:"collabMailTo,omitempty"`
	Invocation   *StructuredInvocation  `json:"invocation,omitempty"`
	Invocations  []StructuredInvocation `json:"invocations,omitempty"`
	Format       string                 `json:"format,omitempty"`
	Attachments  []string               `json:"attachments,omitempty"`
	// Task 234 (upstream #10457 core slice): stable per-attachment identities
	// frozen at enqueue so a dispatch-side schema (disk.go's inline envelope)
	// can round-trip them. Only the field the picked reliability half needs —
	// the upstream attachment package it belongs to does not exist in this
	// fork, so ImageInputs/ImageSourceRefs are deliberately NOT introduced.
	AttachmentIdentities []string      `json:"attachmentIdentities,omitempty"`
	Refs                 []RefSnapshot `json:"refs,omitempty"`
	// FrozenRefBlock is the exact typed reference context rendered at enqueue.
	// FrozenImages contains already-authorized data URLs for direct image input.
	FrozenRefBlock  string            `json:"frozenRefBlock,omitempty"`
	FrozenImages    []string          `json:"frozenImages,omitempty"`
	ReferenceErrors []string          `json:"referenceErrors,omitempty"`
	ExplicitRefs    []string          `json:"explicitRefs,omitempty"`
	Idempotency     string            `json:"idempotencyKey,omitempty"`
	Source          string            `json:"source,omitempty"`
	Extra           map[string]string `json:"extra,omitempty"`
}

// Capacity describes current usage against limits.
type Capacity struct {
	Items        int   `json:"items"`
	MaxItems     int   `json:"maxItems"`
	Bytes        int64 `json:"bytes"`
	MaxBytes     int64 `json:"maxBytes"`
	MaxItemBytes int64 `json:"maxItemBytes"`
}

// InboxSnapshot is the frontend-safe view: metadata only, never full bodies.
type InboxSnapshot struct {
	SchemaVersion int   `json:"schemaVersion"`
	Revision      int64 `json:"revision"`
	Paused        bool  `json:"paused"`
	// 任务709: true only when a human set the current pause. Paused&&!UserPaused
	// is an automatic pause (restart recovery / reopen backlog / error guard)
	// — the state the desktop idle-turn bridge may wake.
	UserPaused  bool            `json:"userPaused,omitempty"`
	Recovered   bool            `json:"recovered"`
	RecoveredN  int             `json:"recoveredCount,omitempty"`
	Readonly    bool            `json:"readonly,omitempty"`
	RunID       string          `json:"runId,omitempty"`
	SessionPath string          `json:"sessionPath,omitempty"`
	Items       []InboxItemMeta `json:"items"`
	Capacity    Capacity        `json:"capacity"`
}

// InboxReceipt is returned after a durable enqueue or admission attempt.
type InboxReceipt struct {
	ItemID      string      `json:"itemId"`
	Disposition Disposition `json:"disposition"`
	Position    int         `json:"position"`
	Paused      bool        `json:"paused"`
	Capacity    Capacity    `json:"capacity"`
	// ByteSize (任务999) is the encoded blob size of the item this receipt
	// names. Fresh enqueues and idempotent hits that found a live item fill
	// it; zero elsewhere (settled-receipt replay, synthesized receipts). The
	// controller's >4 MiB soft-notice reads it — no second blob or manifest
	// lookup just to decide whether to warn.
	ByteSize int64 `json:"byteSize,omitempty"`
	// MergedInto (task 309) is set on an idempotent hit when the original
	// item's content was folded into another queue row by a merge: the
	// receipt still names the surviving row, and this field tells the sender
	// which item their mail ended up inside. Empty = not merged.
	MergedInto string `json:"mergedInto,omitempty"`
	Idempotent bool   `json:"idempotent,omitempty"`
}

// EnqueueRequest is the input for durable admission.
type EnqueueRequest struct {
	Intent      InboxIntent
	Envelope    PromptEnvelope
	Source      string
	Idempotency string
	SessionID   string
}

// Limits configures capacity. Zero fields use defaults.
type Limits struct {
	MaxItems      int
	MaxItemBytes  int64
	MaxTotalBytes int64
}

func (l Limits) withDefaults() Limits {
	if l.MaxItems <= 0 {
		l.MaxItems = DefaultMaxItems
	}
	if l.MaxItemBytes <= 0 {
		l.MaxItemBytes = DefaultMaxItemBytes
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = DefaultMaxTotalBytes
	}
	return l
}
