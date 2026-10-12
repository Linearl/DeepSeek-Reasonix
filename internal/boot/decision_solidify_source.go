package boot

import (
	"sort"
	"strings"
	"time"

	"reasonix/internal/memory"
	"reasonix/internal/tool/builtin"
)

// decisionSolidifySource 把 memory.Store 适配成固化工具的窄数据源接口
// （任务 801 Phase 2）。方向单一：只读，永不删除/改写源记忆（禁区条款）。
// builtin 包不能 import memory（memory → tool 成环），所以适配器住在 boot。
type decisionSolidifySource struct {
	store memory.Store
}

// DecisionMemoriesSince 返回更新时刻晚于 since 的决策记忆条目（decision- 前缀
// 命名空间，Phase 1 提取链的产物），按更新时间升序，供归纳按时间序通读。
func (s decisionSolidifySource) DecisionMemoriesSince(since time.Time) []builtin.DecisionSolidifyItem {
	if s.store.Dir == "" && s.store.GlobalDir == "" {
		return nil
	}
	var out []builtin.DecisionSolidifyItem
	for _, m := range s.store.ListAll() {
		if !strings.HasPrefix(m.Name, "decision-") {
			continue
		}
		if !m.UpdatedAt.After(since) {
			continue
		}
		out = append(out, builtin.DecisionSolidifyItem{
			Name:      m.Name,
			Title:     m.Title,
			Body:      m.Body,
			UpdatedAt: m.UpdatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.Before(out[j].UpdatedAt) })
	return out
}
