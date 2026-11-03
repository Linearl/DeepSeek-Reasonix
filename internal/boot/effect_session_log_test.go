package boot

import (
	"context"
	"encoding/json"
	"testing"

	"reasonix/internal/ablation"
	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// TestSessionResumeThroughRealBuildKeepsModelMessages（原
// TestEffectSessionLogUpgradeKeepsModelMessagesThroughRealBuild）改述：原测试
// 钉「schema-1 → schema-2 升级保模型消息字节不变」——升级目标与 heads 读数都
// 随 schema-2/DAG 头机制退役（tl1227 ListSessionHeads 恒空垫片，tl1280 删
// DAG 写路径，REASONIX_SESSION_LOG 环境钉随 dagSaveRoute 一同失效），升级腿
// 不可达。保留并继续钉住其中独立于格式的红线：真实 Build 栈写下会话 → 换一
// 个 runtime 走 lease+Resume 续跑 → provider 可见转录（含 id）前缀字节不变且
// 继续增长——提示缓存前缀永不被格式/重载改动。
func TestSessionResumeThroughRealBuildKeepsModelMessages(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	rec := &effectRecordingProvider{}
	provider.Register("boot-effect-session-log", func(provider.Config) (provider.Provider, error) {
		return rec, nil
	})
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE"

[environment]
enabled = false

[[providers]]
name = "test-model"
kind = "boot-effect-session-log"
model = "x"
`)
	first, err := Build(context.Background(), Options{Sink: event.Discard, Ablation: ablation.Set{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	first.EnsureSessionPath()
	if err := first.Run(context.Background(), "reply ok"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	path := first.SessionPath()
	before, err := json.Marshal(provider.ModelMessages(first.History()))
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	defer lease.Release()
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	second, err := Build(context.Background(), Options{Sink: event.Discard, Ablation: ablation.Set{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer second.Close()
	second.Resume(loaded, path)
	if err := second.Run(context.Background(), "reply again"); err != nil {
		t.Fatalf("Run after resume: %v", err)
	}
	if err := second.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	after, err := json.Marshal(provider.ModelMessages(second.History()))
	if err != nil {
		t.Fatal(err)
	}
	var beforeMsgs, afterMsgs []json.RawMessage
	if err := json.Unmarshal(before, &beforeMsgs); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after, &afterMsgs); err != nil {
		t.Fatal(err)
	}
	if len(afterMsgs) <= len(beforeMsgs) {
		t.Fatalf("resumed transcript did not grow: before %d after %d", len(beforeMsgs), len(afterMsgs))
	}
	for i := range beforeMsgs {
		if string(beforeMsgs[i]) != string(afterMsgs[i]) {
			t.Fatalf("message %d changed across the schema upgrade\nbefore: %s\nafter:  %s", i, beforeMsgs[i], afterMsgs[i])
		}
	}
	reloaded, err := agent.LoadSession(path)
	if err != nil || len(reloaded.Messages) != len(afterMsgs) {
		t.Fatalf("reload after upgrade: err=%v len=%d want %d", err, len(reloaded.Messages), len(afterMsgs))
	}
}
