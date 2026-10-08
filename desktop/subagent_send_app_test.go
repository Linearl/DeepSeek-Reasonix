package main

// 任务616 desktop surface: SendSubagentMessage (persist-first delivery with
// the three dispositions plus the disabled gate), the pendingMail count in
// the artifact listing, and the mailbox sweep on record delete.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/subagentmailbox"
)

func writeRunningSubagentMeta(t *testing.T, dir, ref, parentSession string, status agent.SubagentStatus) {
	t.Helper()
	subagentDir := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subagentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subagentDir, ref+".jsonl"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := agent.SubagentMeta{
		Ref:           ref,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		Status:        status,
		Kind:          "task",
		Name:          "mail-" + ref,
		WorkspaceRoot: dir,
		ParentSession: parentSession,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subagentDir, ref+".meta.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSendSubagentMessageSteersRunningSubagent(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	ref := "sa_20261008_123000_000000000_000000001111"
	writeRunningSubagentMeta(t, dir, ref, agent.BranchID(sessionPath), agent.SubagentRunning)

	// Simulate the live child: its handle sits in the global registry and
	// consumes the loader like SteerItem would.
	consumed := ""
	un := subagentmailbox.GlobalRegistry.Publish(ref, func(itemID string, load func() (string, error)) bool {
		text, err := load()
		if err != nil {
			t.Fatalf("consume load: %v", err)
		}
		consumed = text
		return true
	})
	defer un()

	receipt, err := app.SendSubagentMessage(sessionPath, ref, "mid-run note", "Also check the tests.")
	if err != nil {
		t.Fatalf("SendSubagentMessage: %v", err)
	}
	if receipt.Disposition != "steered" {
		t.Fatalf("disposition = %q, want steered", receipt.Disposition)
	}
	if receipt.MessageID == "" {
		t.Fatal("receipt missing messageId")
	}
	if consumed != "Also check the tests." {
		t.Fatalf("consumed body = %q", consumed)
	}

	// The listing reports zero pending after consumption.
	views, err := app.ListSubagentsByParent(sessionPath)
	if err != nil {
		t.Fatalf("ListSubagentsByParent: %v", err)
	}
	if len(views) != 1 || views[0].PendingMail != 0 {
		t.Fatalf("views = %+v, want one artifact with zero pending mail", views)
	}
}

func TestSendSubagentMessageParksForEndedSubagent(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	ref := "sa_20261008_123000_000000000_000000002222"
	writeRunningSubagentMeta(t, dir, ref, agent.BranchID(sessionPath), agent.SubagentCompleted)

	receipt, err := app.SendSubagentMessage(sessionPath, ref, "", "Late guidance.")
	if err != nil {
		t.Fatalf("SendSubagentMessage: %v", err)
	}
	if receipt.Disposition != "parked" {
		t.Fatalf("disposition = %q, want parked", receipt.Disposition)
	}
	// Persist-first: the file exists on disk and the listing counts it.
	views, err := app.ListSubagentsByParent(sessionPath)
	if err != nil {
		t.Fatalf("ListSubagentsByParent: %v", err)
	}
	if len(views) != 1 || views[0].PendingMail != 1 {
		t.Fatalf("views = %+v, want pendingMail 1", views)
	}
	mailboxDir := filepath.Join(dir, "subagents", subagentmailbox.MailboxDirName(ref))
	entries, err := os.ReadDir(mailboxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("mailbox dir = %v, %v; want one pending file", entries, err)
	}

	// Deleting the record sweeps the mailbox too (任务616 record cleanup).
	if err := app.DeleteSubagentRecord(sessionPath, ref); err != nil {
		t.Fatalf("DeleteSubagentRecord: %v", err)
	}
	if _, err := os.Stat(mailboxDir); !os.IsNotExist(err) {
		t.Fatalf("mailbox dir survived record delete: %v", err)
	}
}

func TestSendSubagentMessageRefusesForeignAndInvalidTargets(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	foreign := "sa_20261008_123000_000000000_000000003333"
	writeRunningSubagentMeta(t, dir, foreign, "some-other-parent", agent.SubagentRunning)

	if _, err := app.SendSubagentMessage(sessionPath, foreign, "", "x"); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("foreign ref error = %v, want ownership refusal", err)
	}
	if _, err := app.SendSubagentMessage(sessionPath, "", "", "x"); err == nil {
		t.Fatal("empty ref accepted")
	}
	if _, err := app.SendSubagentMessage(sessionPath, "../escape", "", "x"); err == nil {
		t.Fatal("traversal ref accepted")
	}
	if _, err := app.SendSubagentMessage(sessionPath, foreign, "", "   "); err == nil {
		t.Fatal("empty text accepted")
	}
}

func TestSendSubagentMessageDisabledWhenSwitchOff(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	ref := "sa_20261008_123000_000000000_000000004444"
	writeRunningSubagentMeta(t, dir, ref, agent.BranchID(sessionPath), agent.SubagentRunning)

	// Flip the switch off through the user config file (no caching in
	// config.Load — the binding re-reads it per send).
	toml := "[agent]\nexperimental_subagent_messaging = false\n"
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	// Even a live handle must not be steered while the switch is off.
	steered := false
	un := subagentmailbox.GlobalRegistry.Publish(ref, func(itemID string, load func() (string, error)) bool {
		steered = true
		return true
	})
	defer un()

	receipt, err := app.SendSubagentMessage(sessionPath, ref, "", "should not land")
	if err != nil {
		t.Fatalf("SendSubagentMessage (off): %v", err)
	}
	if receipt.Disposition != "disabled" {
		t.Fatalf("disposition = %q, want disabled", receipt.Disposition)
	}
	if steered {
		t.Fatal("handle was steered while the messaging switch is off")
	}
	// Nothing was written to disk.
	mailboxDir := filepath.Join(dir, "subagents", subagentmailbox.MailboxDirName(ref))
	if _, err := os.Stat(mailboxDir); !os.IsNotExist(err) {
		t.Fatalf("mailbox dir created while disabled: %v", err)
	}
}

// Guard: the send tool (parent-side) and the binding share the Hub contract,
// so a queued send keeps its file pending for the continue_from drain.
func TestSendThenContinueDrainRoundTrip(t *testing.T) {
	hub := &subagentmailbox.Hub{Dir: t.TempDir()}
	ref := "sa_20261008_123000_000000000_000000005555"
	receipt, err := hub.Deliver(ref, subagentmailbox.FromUser, "late", "guidance body")
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if receipt.Disposition != subagentmailbox.DispositionParked {
		t.Fatalf("disposition = %q", receipt.Disposition)
	}
	block := hub.DrainForContinue(ref)
	if !strings.Contains(block, "guidance body") {
		t.Fatalf("drain block = %q", block)
	}
}
