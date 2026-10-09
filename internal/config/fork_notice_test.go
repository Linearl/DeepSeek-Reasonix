package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 670 (fork first-launch notice): the switch defaults to ON by explicit
// user ruling (pure UI notice, no behaviour change — 铁律 2 not applicable),
// via the task-265 nil-means-on shape; the user-scope renderer lists all
// three keys (the 81/123 fixed-key-set lesson — an unlisted key is dropped on
// save, which would silently re-arm the dialog or lose the mute); and the
// rendered document round-trips through the loader.
func TestForkNoticeDefaultAndRoundTrip(t *testing.T) {
	c := Default()
	if !c.DesktopForkNoticeEnabled() {
		t.Fatal("fork notice must default to on (user ruling 20261009)")
	}
	// Nil-means-on: the default config must carry no explicit pointer.
	if c.Desktop.ForkNotice != nil {
		t.Fatal("default config must leave fork_notice unset (nil-means-on)")
	}

	if err := c.SetDesktopForkNotice(false); err != nil {
		t.Fatalf("SetDesktopForkNotice(false): %v", err)
	}
	if c.DesktopForkNoticeEnabled() {
		t.Fatal("SetDesktopForkNotice(false) did not take effect")
	}
	if err := c.SetDesktopForkNotice(true); err != nil {
		t.Fatalf("SetDesktopForkNotice(true): %v", err)
	}
	if !c.DesktopForkNoticeEnabled() {
		t.Fatal("SetDesktopForkNotice(true) did not take effect")
	}

	rendered := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(rendered, "fork_notice = true") {
		t.Fatalf("user-scope render dropped fork_notice (81/123 fixed-key-set lesson):\n%s", rendered)
	}
	if !strings.Contains(rendered, "fork_notice_muted") || !strings.Contains(rendered, "fork_notice_last_version") {
		t.Fatalf("user-scope render dropped the fork-notice state keys:\n%s", rendered)
	}
	// Neighbouring chime keys must stay rendered too.
	if !strings.Contains(rendered, "update_chime") {
		t.Fatal("rendering the fork-notice keys displaced the neighbouring update_chime")
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(rendered), 0o600); err != nil {
		t.Fatalf("write rendered config: %v", err)
	}
	loaded := LoadForEdit(path)
	if !loaded.DesktopForkNoticeEnabled() {
		t.Fatal("fork_notice did not survive render/load")
	}

	if err := loaded.SetDesktopForkNotice(false); err != nil {
		t.Fatalf("SetDesktopForkNotice(false) reload: %v", err)
	}
	if strings.Contains(RenderTOMLForScope(loaded, RenderScopeUser), "fork_notice = true") {
		t.Fatal("off switch still renders as true")
	}
}

// Task 670: the 「下次不提醒」 preference and the last-notified version must
// survive a render/save cycle, and re-enabling the switch must clear the
// mute (the lab switch is the only UI path back from a stored mute).
func TestForkNoticeMuteAndAckRoundTrip(t *testing.T) {
	c := Default()
	if err := c.SetDesktopForkNoticeMuted(true); err != nil {
		t.Fatalf("SetDesktopForkNoticeMuted: %v", err)
	}
	if err := c.AcknowledgeForkNoticeVersion("v1.38.3-20261009-1031"); err != nil {
		t.Fatalf("AcknowledgeForkNoticeVersion: %v", err)
	}

	rendered := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(rendered, "fork_notice_muted = true") {
		t.Fatalf("user-scope render dropped fork_notice_muted:\n%s", rendered)
	}
	if !strings.Contains(rendered, `fork_notice_last_version = "v1.38.3-20261009-1031"`) {
		t.Fatalf("user-scope render dropped fork_notice_last_version:\n%s", rendered)
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(rendered), 0o600); err != nil {
		t.Fatalf("write rendered config: %v", err)
	}
	loaded := LoadForEdit(path)
	if !loaded.Desktop.ForkNoticeMuted {
		t.Fatal("fork_notice_muted did not survive render/load")
	}
	if loaded.Desktop.ForkNoticeLastVersion != "v1.38.3-20261009-1031" {
		t.Fatalf("fork_notice_last_version did not survive render/load, got %q", loaded.Desktop.ForkNoticeLastVersion)
	}

	// Re-enable clears the mute: ON must mean the notice actually comes back.
	if err := loaded.SetDesktopForkNotice(true); err != nil {
		t.Fatalf("SetDesktopForkNotice(true): %v", err)
	}
	if loaded.Desktop.ForkNoticeMuted {
		t.Fatal("re-enabling the fork notice must clear the muted preference")
	}
}
