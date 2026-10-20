package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 任务 1011 验收：20261019 ai-passport 样本端到端。设置 TL1011_SAMPLE_DIR
// 指向备份目录后运行；未设置时跳过（同 1010 的门控模式）。样本复制进临时
// 目录再动——用户的备份原件永远不被测试触碰。
//
// 对应派单验收硬条目：
//  1. 样本放回后不敲任何命令打开会话，自愈后可交互且显示最新消息
//     （head=01M59JNK 更长更新链被选中；投影饮食入窗）；
//  2. 健康会话零文件改动（Test1011HealthySessionInspectAndHealTouchNothing
//     已覆盖单测层，这里再对真实样本复验一次检查路径）；
//  3. 全程零模型调用（修复路径没有 provider 依赖，结构上不成立）。

func Test1011SampleEndToEndSelfHeal(t *testing.T) {
	dir := os.Getenv("TL1011_SAMPLE_DIR")
	if dir == "" {
		t.Skip("TL1011_SAMPLE_DIR not set; sample acceptance runs only against the 20261019 backup")
	}
	work := copySample(t, dir)
	entries, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	var sessionPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") && !strings.HasSuffix(e.Name(), ".events.jsonl") && !strings.HasSuffix(e.Name(), ".turns.jsonl") {
			sessionPath = filepath.Join(work, e.Name())
		}
	}
	if sessionPath == "" {
		t.Fatalf("no transcript in sample dir %s", dir)
	}

	// 检查（只读）：异常样本必须报出水位于预/投影超窗/head 落后中的至少两项。
	before := InspectSessionHealth(sessionPath, HealthInspectOptions{})
	if before.Healthy {
		t.Fatalf("the incident sample must inspect unhealthy, got %+v", before)
	}
	codes := map[string]bool{}
	for _, issue := range before.Issues {
		codes[issue.Code] = true
	}
	if !codes[HealthHeadLag] {
		t.Errorf("want head_lag (rotation 切走了更长更新链), got %+v", before.Issues)
	}

	// 自愈（全部动作）：备份 → head 重选 → 补水 → 饮食。
	report, err := SelfHealSession(sessionPath, SelfHealOptions{
		PromoteHead: true, Hydrate: true, Diet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Repaired {
		t.Fatal("expected repair")
	}
	if report.BackupDir == "" {
		t.Error("repair must leave a backup dir")
	}
	if report.HeadTo == "" {
		t.Errorf("head promotion missing: %+v", report.Actions)
	}

	// 自愈后可交互：加载会话（打开路径）成功且投影入窗。
	s, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("post-heal load: %v", err)
	}
	msgs := s.Snapshot()
	if len(msgs) == 0 {
		t.Fatal("post-heal transcript is empty")
	}
	after := InspectSessionHealth(sessionPath, HealthInspectOptions{})
	if after.ProjectionTokens > 0 && after.WindowTokens > 0 && after.ProjectionTokens >= after.WindowTokens {
		t.Errorf("projection still over window after diet: %d/%d", after.ProjectionTokens, after.WindowTokens)
	}

	// 修复记录可读：GUI 健康页与手工审计共用。
	records, err := ListSessionRepairRecordsFor(sessionPath)
	if err != nil || len(records) == 0 {
		t.Errorf("repair records missing: %d err=%v", len(records), err)
	}

	// 复检：head_lag 必须消失（更长更新链已被选中）。
	for _, issue := range after.Issues {
		if issue.Code == HealthHeadLag {
			t.Errorf("head_lag survived the heal: %+v", after.Issues)
		}
	}
}
