package config

import (
	"strings"
	"testing"
)

// 任务 1084 验收：ask 轮次上限 setter 边界（0 或 2~100 生效；1 非法具名拒绝
// 不改写配置；101 拒）。⚠️语义陷阱：0 = 无限制，不是回落默认 3。
func TestSetAskRoundLimitBounds(t *testing.T) {
	c := &Config{}
	sentinel := 3
	c.Desktop.AskRoundLimit = sentinel
	// 1 与 >100 具名拒绝，且不改写配置值。
	for _, bad := range []int{1, 101, 86400, -1} {
		if err := c.SetAskRoundLimit(bad); err == nil {
			t.Fatalf("SetAskRoundLimit(%d) = nil error, want a refusal", bad)
		} else if !strings.Contains(err.Error(), "ask_round_limit") {
			t.Fatalf("refusal for %d should name ask_round_limit, got %q", bad, err.Error())
		}
		if c.Desktop.AskRoundLimit != sentinel {
			t.Fatalf("refused value %d was stored anyway (field = %d)", bad, c.Desktop.AskRoundLimit)
		}
	}
	// 边界 2/100 生效；0 生效（无限制）。
	for _, good := range []int{2, 100, 0} {
		if err := c.SetAskRoundLimit(good); err != nil {
			t.Fatalf("SetAskRoundLimit(%d) = %v, want accepted", good, err)
		}
		if c.Desktop.AskRoundLimit != good {
			t.Fatalf("stored %d, want %d", c.Desktop.AskRoundLimit, good)
		}
	}
}

// 任务 1084 验收：AskRoundLimitEffective——0 原样返回 0（无限制，不回落默认
// 3）；Default() 打底 3；显式值原样返回。
func TestAskRoundLimitEffectiveZeroIsUnlimited(t *testing.T) {
	var nilCfg *Config
	if got := nilCfg.AskRoundLimitEffective(); got != AskRoundLimitDefault {
		t.Fatalf("nil config effective = %d, want default %d", got, AskRoundLimitDefault)
	}
	// Default() 打底 3（新装用户默认 3）。
	if got := Default().AskRoundLimitEffective(); got != AskRoundLimitDefault {
		t.Fatalf("Default() effective = %d, want default %d", got, AskRoundLimitDefault)
	}
	// 显式 0 = 无限制，原样返回 0 不回落 3（与 task 477 语义相反）。
	zero := &Config{}
	zero.Desktop.AskRoundLimit = 0
	if got := zero.AskRoundLimitEffective(); got != 0 {
		t.Fatalf("explicit 0 effective = %d, want 0 (unlimited, not the default)", got)
	}
	five := &Config{}
	five.Desktop.AskRoundLimit = 5
	if got := five.AskRoundLimitEffective(); got != 5 {
		t.Fatalf("explicit 5 effective = %d, want 5", got)
	}
}

// 任务 1084 验收：五个授权 checkbox 默认全不勾（出厂五类全要问，保守）。
func TestFiveAuthorizeDefaultsAllUnset(t *testing.T) {
	c := &Config{}
	if c.Desktop.AuthorizeOutboundSend || c.Desktop.AuthorizeDataMigrationDelete ||
		c.Desktop.AuthorizeDestructiveChange || c.Desktop.AuthorizeNewTechRoute ||
		c.Desktop.AuthorizePaidQuotaAlloc {
		t.Fatalf("全新配置五个授权 checkbox 应全不勾，got out=%v mig=%v dest=%v route=%v quota=%v",
			c.Desktop.AuthorizeOutboundSend, c.Desktop.AuthorizeDataMigrationDelete,
			c.Desktop.AuthorizeDestructiveChange, c.Desktop.AuthorizeNewTechRoute,
			c.Desktop.AuthorizePaidQuotaAlloc)
	}
	// 逐类独立：勾「数据迁移与删除」后该类为 true，其余四类仍 false。
	if err := c.SetAuthorizeDataMigrationDelete(true); err != nil {
		t.Fatalf("SetAuthorizeDataMigrationDelete(true): %v", err)
	}
	if !c.Desktop.AuthorizeDataMigrationDelete {
		t.Fatalf("勾选后数据迁移与删除应为 true")
	}
	if c.Desktop.AuthorizeOutboundSend || c.Desktop.AuthorizeDestructiveChange ||
		c.Desktop.AuthorizeNewTechRoute || c.Desktop.AuthorizePaidQuotaAlloc {
		t.Fatalf("勾一类不应连带其余四类")
	}
}
