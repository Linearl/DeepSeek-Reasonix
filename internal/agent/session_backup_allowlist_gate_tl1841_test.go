package agent

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"reasonix/internal/store"
)

// tl1841 ②（d1 扫描 G2）：差集门禁——「会话工件清单 × 备份白名单」的差集
// 新增即红。机制史：tl1442-ext（zst 存储形态）、本次 splices/rewritelog/
// conflict-preserved（三个新数据侧车只登删除清单没跟归档白名单）——同一
// 元同步缺口三连复发。本文件把「新增数据侧车必须同 commit 登记两处清单」
// 从「靠记住」变成「跑测试报红」。
//
// 机械形态选型说明：差集的机械本质是「跑 Go 构造器、对比两个 Go 集合」，
// node 锚段（check-fork-integrity）只能钉「字符串存在」钉不住差集属性；
// 仓内先例（TestSessionPathSpellingsSingleSource、tl1442-ext 键集同 commit
// 纪律）都是 Go 守卫测试。故本门禁落包级测试；check-fork-integrity 另补
// 锚段防上游 merge 静默丢行（该脚本职责即 fork-only 特征锚）。
//
// 存量欠账口径：静态换位清单里 guardian 家族的派生数据侧车只有核心 6 键
// 在白名单（meta/events/转录/双 index/cursor），其余 11 键是本次门禁首跑
// 实测出的存量缺口（G2 同机制存量面，非本次任务书新增侧车）。按「上限+
// 新增即红」纪律显式登记在 exemptions 里：扩权（补登）或收窄（清单裁剪）
// 都要动这份登记，静默新增必红。

// tl1841AllowlistExemptions 是差集门禁的显式豁免登记（键=stem 相对 rel）。
// 每个键必须给出理由；豁免被清（对应 rel 从清单消失）后应同步删键。
var tl1841AllowlistExemptions = map[string]string{
	// 任务 1116 终裁：修复记录是审计轨迹不是数据侧车（白名单天然排除）。
	"repairlog.jsonl":          "任务1116 终裁：审计轨迹不入备份白名单",
	"guardian.repairlog.jsonl": "任务1116 终裁：审计轨迹不入备份白名单（guardian 家族）",
	// 目录工件：backupSessionFiles 按构造跳目录（e.IsDir()），不在复制语义内。
	"ckpt":          "目录工件：备份函数按构造跳目录",
	"jobs":          "目录工件：备份函数按构造跳目录",
	"inbox":         "目录工件：备份函数按构造跳目录",
	"reminders":     "目录工件：备份函数按构造跳目录",
	"jsonl.imgpack": "目录工件：备份函数按构造跳目录（legacy 无写入方）",
	// 存量欠账（tl1841 门禁首跑实测登记；G2 同机制存量面，扩权需拍板）：
	// guardian 家族派生数据侧车从未入过归档白名单——转正换位链对它们同样
	// 「无归档销毁」。本次任务书只收口三个新增侧车；存量扩权留后续裁决。
	"guardian.goal-state.json":       "存量欠账 tl1841 登记：guardian 家族数据侧车未入白名单",
	"guardian.events.jsonl.zst":      "存量欠账 tl1841 登记：guardian 家族数据侧车未入白名单",
	"guardian.events.jsonl.damaged":  "存量欠账 tl1841 登记：guardian 家族数据侧车未入白名单",
	"guardian.events.jsonl.rotating": "存量欠账 tl1841 登记：guardian 家族数据侧车未入白名单",
	"guardian.jsonl.zst":             "存量欠账 tl1841 登记：guardian 家族数据侧车未入白名单",
	"guardian.turns.jsonl":           "存量欠账 tl1841 登记：guardian 家族数据侧车未入白名单",
	"guardian.turns.jsonl.damaged":   "存量欠账 tl1841 登记：guardian 家族数据侧车未入白名单",
	"guardian.conflicts.jsonl":       "存量欠账 tl1841 登记：guardian 家族数据侧车未入白名单",
	"guardian.recovery.json":         "存量欠账 tl1841 登记：guardian 家族数据侧车未入白名单",
	"guardian.context.json":          "存量欠账 tl1841 登记：guardian 家族数据侧车未入白名单",
	"guardian.pinned-context.json":   "存量欠账 tl1841 登记：guardian 家族数据侧车未入白名单",
}

// tl1841StaticInventoryRels 跑 store 静态换位清单构造器（纯字符串运算，
// 不触盘），把每件工件折成 backupSessionFiles 同款 rel 键
// （TrimPrefix(name, stemBase+".")，见 session_selfheal.go 白名单准入注释）。
func tl1841StaticInventoryRels(t *testing.T) []string {
	t.Helper()
	const stemBase = "gate-probe"
	probe := filepath.Join(t.TempDir(), stemBase+store.SessionTranscriptSuffix)
	rels := make([]string, 0, 64)
	for _, artifact := range store.SessionStaticTrashSidecars(probe) {
		rels = append(rels, strings.TrimPrefix(filepath.Base(artifact), stemBase+"."))
	}
	return rels
}

// tl1841AllowlistGaps 返回清单 rel 里既不在 sessionBackupAllowlist 也不在
// 豁免登记的键——差集门禁的机械核心（新增即红的「红」从这出来）。
func tl1841AllowlistGaps(rels []string) []string {
	var gaps []string
	for _, rel := range rels {
		if _, allowed := sessionBackupAllowlist[rel]; allowed {
			continue
		}
		if _, exempt := tl1841AllowlistExemptions[rel]; exempt {
			continue
		}
		gaps = append(gaps, rel)
	}
	sort.Strings(gaps)
	return gaps
}

// TestSessionBackupAllowlistGateCoversStaticInventory tl1841② 绿面：静态换位
// 清单每一件（含 guardian 全家派生）都必须在白名单或显式豁免里——三个新增
// 数据侧车（conflict-preserved/splices/rewritelog，main+guardian 两族）同
// commit 补登后此测试才绿。
func TestSessionBackupAllowlistGateCoversStaticInventory(t *testing.T) {
	if gaps := tl1841AllowlistGaps(tl1841StaticInventoryRels(t)); len(gaps) > 0 {
		t.Fatalf("session sidecar inventory has keys missing from the backup allowlist (new data sidecars must register both lists in the same commit): %v", gaps)
	}
	// 主转录自身（rel "jsonl"）是备份的核心内容：白名单丢了它备份整体失败。
	if _, ok := sessionBackupAllowlist["jsonl"]; !ok {
		t.Fatal("allowlist lost the main transcript admission key \"jsonl\" — backups would come up empty and fail")
	}
}

// TestSessionBackupAllowlistGateRedOnInjectedSidecar tl1841② 红面自测：注入
// 一个未来形态的新侧车 rel，首行与非首行两个位置都必须报红；移除注入即回绿。
// 这是门禁自身「新增即红」属性的验收（防门禁退化为恒绿摆设）。
func TestSessionBackupAllowlistGateRedOnInjectedSidecar(t *testing.T) {
	inventory := tl1841StaticInventoryRels(t)
	const injected = "futuresidecar.json"

	// 注入首行。
	first := append([]string{injected}, inventory...)
	if gaps := tl1841AllowlistGaps(first); len(gaps) != 1 || gaps[0] != injected {
		t.Fatalf("injected sidecar at the first position must gate-red: gaps=%v", gaps)
	}
	// 注入非首行（夹在清单中段）。
	mid := len(inventory) / 2
	midInventory := append(append([]string(nil), inventory[:mid]...), injected)
	midInventory = append(midInventory, inventory[mid:]...)
	if gaps := tl1841AllowlistGaps(midInventory); len(gaps) != 1 || gaps[0] != injected {
		t.Fatalf("injected sidecar at a non-first position must gate-red: gaps=%v", gaps)
	}
	// 移除注入即回绿。
	if gaps := tl1841AllowlistGaps(inventory); len(gaps) != 0 {
		t.Fatalf("clean inventory must gate-green after removing the injection: gaps=%v", gaps)
	}
}

// TestSessionBackupAllowlistGateExemptionsAllConsumed 豁免登记卫生：每个豁免
// 键都必须被当前静态清单实际消费——清单侧裁掉某工件后，对应豁免必须同 commit
// 删键（防豁免表腐化成第二张失真清单）。
func TestSessionBackupAllowlistGateExemptionsAllConsumed(t *testing.T) {
	consumed := map[string]bool{}
	for _, rel := range tl1841StaticInventoryRels(t) {
		consumed[rel] = true
	}
	for rel := range tl1841AllowlistExemptions {
		if !consumed[rel] {
			t.Fatalf("exemption %q is no longer produced by store.SessionStaticTrashSidecars — remove the exemption in the same commit that trimmed the inventory", rel)
		}
	}
}
