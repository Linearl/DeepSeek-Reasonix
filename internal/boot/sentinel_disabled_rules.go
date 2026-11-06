package boot

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
)

// tl1400: Sentinel disabled_rules 会话 Notice 去重。
//
// 设计意图（852 调研建议 #2 / tl896）是「装配期一次性告知哪些硬底线被配置
// 关掉了」——静默关底线是配置面最危险的态。但 boot.Build 每次会话装配都跑，
// 无条件 Emit 使同一 disabled_rules 集合每个新会话都重复弹警告条（用户现场
// 2026-11-05：有意配置的 write_system_config 禁用每会话重复提示）。这里在
// 进程级记录「上次已告知的集合」，集合相同则静默；集合变化（增/删 id，与
// 顺序、重复项无关）或非空→空（用户删配置恢复保护）才再发。
//
// 两条边界：
//   - 装配期 slog WARN（boot.go sentinel 装配处）不去重——日志可检索性不受
//     去重影响，这里只管用户可见的会话 Notice；
//   - 禁用告知文案逐字沿用 tl896 引入时的字符串（既有 Notice 文案模式），
//     恢复告知是新文案，走 i18n 三语目录，正向消息用 info。
//
// [sentinel] 是 user-global 配置（项目 reasonix.toml 不可覆盖，见装配处
// 同款注释），并发 workspace 的 Build 拿到同一集合，包级状态 + 互斥锁即
// 正确落点；测试用 resetSentinelDisabledRulesForTest 清零保证顺序无关。
type sentinelDisabledRulesAction int

const (
	// sentinelDisabledRulesQuiet：本次无需发 Notice——该集合已告知过（与顺序、
	// 重复项无关），或从未告知过且本次为空（无「恢复」可言）。
	sentinelDisabledRulesQuiet sentinelDisabledRulesAction = iota
	// sentinelDisabledRulesWarnDisabled：非空集合且与上次告知的不同，发禁用告知。
	sentinelDisabledRulesWarnDisabled
	// sentinelDisabledRulesRestored：上次告知过非空集合、本次为空，发恢复告知。
	sentinelDisabledRulesRestored
)

// sentinelDisabledRulesState 记录本进程上次已告知的 disabled_rules 集合。
type sentinelDisabledRulesState struct {
	mu        sync.Mutex
	announced bool                // 是否已告知过任何集合（含空集合）
	last      map[string]struct{} // 上次告知的集合（规范化后）
}

// processSentinelDisabledRules 供每次 boot.Build 共用（进程级单例）。
var processSentinelDisabledRules sentinelDisabledRulesState

// normalizeDisabledRuleKeys 与 sentinel.SetDisabledRules 同一规范化
//（TrimSpace + 小写、去空串），使集合比较与 sentinel 的生效语义一致：
// 大小写/空白差异不算集合变化，重复项折叠。
func normalizeDisabledRuleKeys(rules []string) map[string]struct{} {
	key := make(map[string]struct{}, len(rules))
	for _, r := range rules {
		if r = strings.ToLower(strings.TrimSpace(r)); r != "" {
			key[r] = struct{}{}
		}
	}
	return key
}

// observe 记录本次 build 的 disabled_rules 集合并判定本次会话 Notice 该
// 说什么。同一集合（与顺序、重复项无关）返回 Quiet；集合变化返回
// WarnDisabled；非空→空返回 Restored（prev 为上次集合成员，排序后供文案
// 引用）。判定与记录在同一临界区内完成，并发 Build 只有一家拿到非 Quiet。
func (s *sentinelDisabledRulesState) observe(rules []string) (sentinelDisabledRulesAction, []string) {
	key := normalizeDisabledRuleKeys(rules)
	s.mu.Lock()
	defer s.mu.Unlock()
	wasAnnounced, prev := s.announced, s.last
	if wasAnnounced && stringSetsEqual(prev, key) {
		return sentinelDisabledRulesQuiet, nil
	}
	s.announced, s.last = true, key
	switch {
	case len(key) > 0:
		return sentinelDisabledRulesWarnDisabled, nil
	case wasAnnounced && len(prev) > 0:
		return sentinelDisabledRulesRestored, sortedStringSet(prev)
	default:
		// 进程内首次观察且为空：保护本来就在，没有「恢复」可宣告。
		return sentinelDisabledRulesQuiet, nil
	}
}

func stringSetsEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func sortedStringSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// emitSentinelDisabledRulesNotice 按 observe 的判定发本次会话 Notice（见
// 类型注释：同集合去重、集合变化重发、非空→空发恢复）。
func emitSentinelDisabledRulesNotice(sink event.Sink, rules []string) {
	action, restored := processSentinelDisabledRules.observe(rules)
	switch action {
	case sentinelDisabledRulesWarnDisabled:
		// 文案与 tl896 引入时逐字一致——只修通知频率，不动既有文案。
		sink.Emit(event.Event{
			Kind:  event.Notice,
			Level: event.LevelWarn,
			Text:  "Sentinel 硬规则已被配置禁用。",
			Detail: fmt.Sprintf("[sentinel].disabled_rules 当前禁用了这些硬底线规则：%s。"+
				"这些规则本应在任何审批模式（含 yolo）下都拦截；如非有意，请删除配置中对应 id 恢复保护。",
				strings.Join(rules, ", ")),
		})
	case sentinelDisabledRulesRestored:
		sink.Emit(event.Event{
			Kind:  event.Notice,
			Level: event.LevelInfo,
			Text:  i18n.M.SentinelDisabledRulesRestored,
			Detail: fmt.Sprintf(i18n.M.SentinelDisabledRulesRestoredFmt,
				strings.Join(restored, ", ")),
		})
	case sentinelDisabledRulesQuiet:
	}
}

// resetSentinelDisabledRulesForTest 清零进程级告知记录。boot 包内涉及
// disabled_rules 的测试在开头与 Cleanup 各调一次，保证测试互不影响、
// go test -race -count=2 复跑同样成立。
func resetSentinelDisabledRulesForTest() {
	processSentinelDisabledRules.mu.Lock()
	defer processSentinelDisabledRules.mu.Unlock()
	processSentinelDisabledRules.announced = false
	processSentinelDisabledRules.last = nil
}
