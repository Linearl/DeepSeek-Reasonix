package config

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// tl1598 — 宪法 IV「开关三终局」落账门禁（违宪审查 20261115 违宪-1 整改）。
//
// 审查发现源码 experimental_* 定义全集 108 键中 11 个未落任何登记表
// （档位表 labFeatureTiers ∪ 实验室豁免 labNonFeatureKeys ∪ 渲染豁免
// desktopRenderOmissions = 97），且因不在表内而清不到双周评审视野——
// 「账外欠条清不到」。本门禁把「新增 experimental_* 键必须落账」做成
// 机器断言（E4 修完补防线），手法对齐 tl1265 批量卫生门禁
// （TestLabBulkSettersRegistryHygiene：扫描注册表，表外即红，失败信息
// 点名要求显式登记）。
//
// 定义面口径：生产 toml 定义仅 config.go 与 desktop_preferences.go 两个
// 文件（20261116 git grep 核验），均在 Config struct 树上，故 reflect 遍历
// Default() 即完整定义面（agent/session_control_test.go 等测试文件里的
// 本地 struct tag 非生产定义点，不入口径）。

// collectExperimentalTOMLKeys 递归收集 struct 树上全部 experimental_ 前缀的
// toml 键（含 Ptr/Slice/Map/Array 元素类型；seen 防环）。排序返回保证失败
// 信息稳定可 diff。
func collectExperimentalTOMLKeys(root any) []string {
	seen := map[reflect.Type]bool{}
	var keys []string
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice ||
			t.Kind() == reflect.Map || t.Kind() == reflect.Array {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || seen[t] {
			return
		}
		seen[t] = true
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if tag := f.Tag.Get("toml"); tag != "" && tag != "-" {
				key := strings.Split(tag, ",")[0]
				if strings.HasPrefix(key, "experimental_") {
					keys = append(keys, key)
				}
			}
			walk(f.Type)
		}
	}
	walk(reflect.TypeOf(root))
	sort.Strings(keys)
	return keys
}

// unregisteredExperimentalKeys 返回未落三张登记表的键（宪法 IV 判定链：
// labFeatureTiers.renderKeys ∪ labNonFeatureKeys ∪ desktopRenderOmissions）。
func unregisteredExperimentalKeys(keys []string) []string {
	registered := map[string]bool{}
	for _, f := range labFeatureTiers {
		for _, k := range f.renderKeys {
			registered[k] = true
		}
	}
	for k := range labNonFeatureKeys {
		registered[k] = true
	}
	for k := range desktopRenderOmissions {
		registered[k] = true
	}
	var missing []string
	for _, k := range keys {
		if !registered[k] {
			missing = append(missing, k)
		}
	}
	return missing
}

// TestExperimentalKeysAllRegisteredInLedgers —— 正例门禁：Config 树上每个
// experimental_* 键必须落三表之一，任一键账外即红。双防御：
//   - 键数下限 108 = 20261116 git grep 对账全集（违宪审查快照同数）；扫描器
//     若漏扫某个定义区，键数跌破下限立刻红（防假阴性）。退役删键合法跌破
//     时随删键 commit 顺手下调本数字——删键留痕，免费的清账哨兵；
//   - 空集防御并入下限（0 < 108 同样触发）。
func TestExperimentalKeysAllRegisteredInLedgers(t *testing.T) {
	keys := collectExperimentalTOMLKeys(Default())
	if len(keys) < 108 {
		t.Fatalf("collectExperimentalTOMLKeys(Default()) 只扫到 %d 个 experimental_* 键（下限 108 = 20261116 对账全集）——扫描器漏扫，本门禁不可信", len(keys))
	}
	if missing := unregisteredExperimentalKeys(keys); len(missing) > 0 {
		t.Fatalf("这些 experimental_* 键未落任何登记表（宪法 IV：新实验键必须入 labFeatureTiers / labNonFeatureKeys / desktopRenderOmissions 之一并逐键给理由——账外欠条清不到，违宪审查 20261115 违宪-1）: %v", missing)
	}
}

// TestExperimentalLedgerGateCatchesUnregisteredKey —— 反例门禁：构造一个
// 表外键走同一扫描+判定链，必须被点名。守的是门禁本身不恒绿（只测正例
// 的断言形同虚设：判定链写错时正例照样过）。
func TestExperimentalLedgerGateCatchesUnregisteredKey(t *testing.T) {
	fake := struct {
		ExperimentalNotInAnyLedger bool `toml:"experimental_not_in_any_ledger"`
	}{}
	missing := unregisteredExperimentalKeys(collectExperimentalTOMLKeys(fake))
	if len(missing) != 1 || missing[0] != "experimental_not_in_any_ledger" {
		t.Fatalf("判定链未能点名构造的表外键 experimental_not_in_any_ledger (got %v) —— 门禁失效", missing)
	}
}
