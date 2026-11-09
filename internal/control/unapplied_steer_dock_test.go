package control

// tl1357b 必做2 · 复现钉：RecordUnappliedSteer 无 itemID 面的丢失面。
//
// 机制面：loader 失败兜底（run_loop.go）与 steer fallback 兜底把未投递引导记为
// unapplied——但 itemID 为空时（无持久行：内存 face / 停靠缺失），文本只落在
// agent 内存 transcript（LocalOnly）+ 一条警告事件，进程退出即丢，shelf 上无
// 任何可检索可重发的持久行。修后（绿）：无 itemID 的 unapplied steer 必须在
// manifest 停靠一行（intent=steer、uncertain、SteerUnapplied——永不自动派发，
// #7045 语义保持，Retry 重发是唯一回队路径）。
//
// 撞面钉：itemID 面保持原语义——已有持久行的 unapplied 通知走
// onInboxUnappliedSteer（停靠既有行），不得再 dock 第二行。

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioninbox"
	"reasonix/internal/tool"
)

// dockHarness: controller with a real durable store and a bound executor —
// the face RecordUnappliedSteer needs.
func newDockHarness(t *testing.T) (*Controller, *agent.Agent) {
	t.Helper()
	dir := t.TempDir()
	prov := &scriptedTurns{turns: [][]provider.Chunk{textTurn("ok")}}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession(""), agent.Options{}, event.Discard)
	c := New(Options{
		Runner:      ag,
		Executor:    ag,
		Sink:        event.Discard,
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(func() {
		c.Close()
		c.autosaveWG.Wait()
	})
	return c, ag
}

func dockManifestItems(t *testing.T, c *Controller) []sessioninbox.InboxItemMeta {
	t.Helper()
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	return st.Snapshot().Items
}

func waitForDockRow(t *testing.T, c *Controller, want string) sessioninbox.InboxItemMeta {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		items := dockManifestItems(t, c)
		if len(items) == 1 {
			item := items[0]
			if item.State != sessioninbox.StateUncertain {
				t.Fatalf("停靠行状态 = %s, want uncertain（不自动派发）", item.State)
			}
			if item.Intent != sessioninbox.IntentSteer {
				t.Fatalf("停靠行意图 = %s, want steer", item.Intent)
			}
			if item.BlockReason != sessioninbox.BlockReasonSteerUnapplied {
				t.Fatalf("停靠行原因 = %q, want %q", item.BlockReason, sessioninbox.BlockReasonSteerUnapplied)
			}
			st, err := c.ensureInbox()
			if err != nil {
				t.Fatal(err)
			}
			_, env, err := st.ReadItem(item.ID)
			if err != nil {
				t.Fatalf("停靠行正文必须可检索: %v", err)
			}
			if !strings.Contains(env.SubmitText, want) {
				t.Fatalf("停靠行正文 = %q, want it to contain %q", env.SubmitText, want)
			}
			return item
		}
		if time.Now().After(deadline) {
			t.Fatalf("无 itemID 的 unapplied steer 未停靠 manifest 行（修前红：无任何持久行），行数 = %d", len(items))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 钉1：RecordUnappliedSteer 无 itemID（内存 face / 停靠缺失面）必须在 manifest
// 停靠可检索可重发的行。修前（红）零持久行。
func TestUnappliedSteerDocksManifestRow(t *testing.T) {
	c, ag := newDockHarness(t)

	const guidance = "tl1357b必做2：无持久行的未投递引导必须停靠"
	ag.RecordUnappliedSteer(guidance)

	waitForDockRow(t, c, guidance)
}

// 钉2（撞面）：带 itemID 的 unapplied 通知走既有停靠面（onInboxUnappliedSteer
// 翻既有行 uncertain），不得再 dock 第二行。
func TestUnappliedSteerWithItemIDParksExistingRowOnly(t *testing.T) {
	c, ag := newDockHarness(t)

	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	// 既有持久行（形态① steer_accepted 面）。
	rec, err := c.EnqueueInbox(InboxRequest{
		Intent:  sessioninbox.IntentSteer,
		Display: "tl1357b钉2：既有行",
		Raw:     "tl1357b钉2：既有行",
		Submit:  "tl1357b钉2：既有行",
		Source:  "steer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(rec.ItemID, sessioninbox.StateSteerAccepted, ""); err != nil {
		t.Fatal(err)
	}

	const guidance = "tl1357b钉2：带 itemID 的未投递引导"
	ag.RecordUnappliedSteer(guidance, rec.ItemID)

	deadline := time.Now().Add(10 * time.Second)
	for {
		items := dockManifestItems(t, c)
		if len(items) == 1 && items[0].State == sessioninbox.StateUncertain {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("既有行必须翻 uncertain 且不得新增停靠行，items = %+v", dockManifestItems(t, c))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
