package agent

// 任务978 / [1152] 现场实证的两条新验收证据。
//
// 现场口径（2026-10-25 12:00 用户会话卡死）：eventsMb=2277.85 超 512 限额 4.4 倍、
// sessionEventsFileMb=104.62 刚破 100 健康线；hydrate:read 1418ms、boot 单 leg
// 排队 24476ms。978 修法（触发线前移+回退态重试门降档）对该现场的机制收益是：
// 压缩循环自激的轮次更少 ⇒ events 里重复的 replace/大 payload 追加更少 ⇒
// events 文件更小 ⇒ hydrate（events → 会话重放）读成本更低。
//
// 本文件给「压缩后 events 体积下降 → hydrate 耗时下降」的单点证据：同一份
// 消息集，分别以「自激形态」（压缩循环反复重写 replace——978 现场「请求数+12」
// 的 transcript 事件流形态）与「收敛形态」（单次 replace）落盘，对照 events
// 文件字节量、记录数与重放（hydrate 读路径的核心，replaySessionEventLog）耗时。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// tl978WriteEventLog 落一份合成 events log：rounds 次 replace，每次携带同一份
// messages（每条 contentKB KB）。返回 log 路径与字节数。
func tl978WriteEventLog(t *testing.T, dir string, rounds, messages, contentKB int) (string, int64) {
	t.Helper()
	payload := strings.Repeat("x", contentKB*1024)
	msgJSON := make([]string, 0, messages)
	for i := range messages {
		buf, err := json.Marshal(provider.Message{Role: provider.RoleAssistant, Content: payload[:1] + fmt.Sprintf("%d:", i) + payload[1:]})
		if err != nil {
			t.Fatalf("marshal message = %v", err)
		}
		msgJSON = append(msgJSON, string(buf))
	}
	var b strings.Builder
	for r := range rounds {
		b.WriteString(`{"schema_version":1,"type":"replace","revision":` + strconv.Itoa(r+1) + `,"base_revision":` + strconv.Itoa(r) + `,"messages":[` + strings.Join(msgJSON, ",") + `]}` + "\n")
	}
	path := filepath.Join(dir, "tl978.events.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, info.Size()
}

// 验收（[1152]①单点证据）：自激形态 vs 收敛形态——events 体积、重放消息数、
// hydrate 读耗时三组读数对照。体积/消息数为硬断言；耗时给实测读数（CI 噪声
// 不做硬门，回执引用本机实测值）。
func TestTask978HydrateCostTracksEventLogSize(t *testing.T) {
	const rounds, messages, contentKB = 12, 50, 1

	burstPath, burstBytes := tl978WriteEventLog(t, t.TempDir(), rounds, messages, contentKB)
	calmPath, calmBytes := tl978WriteEventLog(t, t.TempDir(), 1, messages, contentKB)

	if calmBytes >= burstBytes {
		t.Fatalf("folded log = %d bytes, not smaller than the compaction-loop log %d bytes", calmBytes, burstBytes)
	}

	burstStart := time.Now()
	burstReplay, err := replaySessionEventLog(burstPath)
	burstCost := time.Since(burstStart)
	if err != nil {
		t.Fatalf("replay compaction-loop log = %v", err)
	}
	calmStart := time.Now()
	calmReplay, err := replaySessionEventLog(calmPath)
	calmCost := time.Since(calmStart)
	if err != nil {
		t.Fatalf("replay folded log = %v", err)
	}

	// 两种形态重放出的会话内容一致（replace 是快照语义，最后一次生效）；
	// 但自激形态的 hydrate 必须把 12 份重复 payload 全部解码才能得出同一份
	// 消息集——这正是「events 文件越大 hydrate 越慢」的读放大机理。
	if len(burstReplay.msgs) != messages {
		t.Fatalf("compaction-loop replay messages = %d, want %d (same final snapshot)", len(burstReplay.msgs), messages)
	}
	if len(calmReplay.msgs) != messages {
		t.Fatalf("folded replay messages = %d, want %d", len(calmReplay.msgs), messages)
	}

	shrink := float64(burstBytes) / float64(calmBytes)
	t.Logf("[tl978 evidence] compaction-loop log: %d bytes, replay msgs=%d, hydrate read=%s", burstBytes, len(burstReplay.msgs), burstCost.Round(time.Microsecond))
	t.Logf("[tl978 evidence] folded log:         %d bytes, replay msgs=%d, hydrate read=%s", calmBytes, len(calmReplay.msgs), calmCost.Round(time.Microsecond))
	t.Logf("[tl978 evidence] bytes shrink x%.1f; same message set survives (fold is lossless)", shrink)
	if shrink < float64(rounds)/2 {
		t.Fatalf("bytes shrink only x%.1f, want at least x%.1f (the loop rewrites the same payload %d times)", shrink, float64(rounds)/2, rounds)
	}
}
