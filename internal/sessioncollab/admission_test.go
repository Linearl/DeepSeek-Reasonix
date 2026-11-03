package sessioncollab

// tl1340：准入策略表测试。
//
// 验收锚点：
//  1. 全枚举——4 源 × 3 名单 × 2 @ = 24 格逐一断言 verdict（表驱动，一格不落），
//     并同时钉住 AdmissionRuleCount 与真实表尺寸一致（渲染引用行引用的数字）。
//  2. 纯函数确定性——同一输入重复判定结果全等，且判定不随调用顺序漂移。
//  3. 词表卫生——新终局值 ignored_admission 进 ValidDeliveryReceiptOutcome、
//     结算是终态（非 failed_retrying）、分级为无需行动（静默通道）。
//  4. 来源折叠——五值身份枚举 → 三源+unknown 的映射逐值断言，单向章覆盖。
//  5. 名单分类——deny 优先于 allow；空/未登记 → none。
//  6. @提及检测——contactId 与标题两路命中、大小写不敏感、空名不匹配、
//     RequireReply 语义无关（检测只看结构化寻址）。

import (
	"strings"
	"testing"
)

// TestAdmissionTableFullEnumeration 是策略表的验收主体：24 格全枚举。
// 表内容若要调整，必须同步改这里的期望值并 bump AdmissionPolicyVersion。
func TestAdmissionTableFullEnumeration(t *testing.T) {
	type cell struct {
		source   AdmissionSource
		list     AdmissionList
		mention  bool
		expected AdmissionVerdict
	}
	cells := []cell{
		// ── user：人类来源，点名/白名单才 explicit，广播 inferred，黑名单 ignore ──
		{AdmissionSourceUser, AdmissionListAllow, true, AdmissionExplicit},
		{AdmissionSourceUser, AdmissionListAllow, false, AdmissionExplicit},
		{AdmissionSourceUser, AdmissionListNone, true, AdmissionExplicit},
		{AdmissionSourceUser, AdmissionListNone, false, AdmissionInferred},
		{AdmissionSourceUser, AdmissionListDeny, true, AdmissionIgnore},
		{AdmissionSourceUser, AdmissionListDeny, false, AdmissionIgnore},
		// ── agent：协作代理，同 user 形状 ──
		{AdmissionSourceAgent, AdmissionListAllow, true, AdmissionExplicit},
		{AdmissionSourceAgent, AdmissionListAllow, false, AdmissionExplicit},
		{AdmissionSourceAgent, AdmissionListNone, true, AdmissionExplicit},
		{AdmissionSourceAgent, AdmissionListNone, false, AdmissionInferred},
		{AdmissionSourceAgent, AdmissionListDeny, true, AdmissionIgnore},
		{AdmissionSourceAgent, AdmissionListDeny, false, AdmissionIgnore},
		// ── system：系统通知，白名单才 explicit；无名单时只有点名才送（inferred），
		//    无点名的系统广播 ignore（tl1058：单向通知不得未经邀请进模型上下文）──
		{AdmissionSourceSystem, AdmissionListAllow, true, AdmissionExplicit},
		{AdmissionSourceSystem, AdmissionListAllow, false, AdmissionExplicit},
		{AdmissionSourceSystem, AdmissionListNone, true, AdmissionInferred},
		{AdmissionSourceSystem, AdmissionListNone, false, AdmissionIgnore},
		{AdmissionSourceSystem, AdmissionListDeny, true, AdmissionIgnore},
		{AdmissionSourceSystem, AdmissionListDeny, false, AdmissionIgnore},
		// ── unknown：白名单（contact 级信任）仍 explicit，其余 fail-closed ──
		{AdmissionSourceUnknown, AdmissionListAllow, true, AdmissionExplicit},
		{AdmissionSourceUnknown, AdmissionListAllow, false, AdmissionExplicit},
		{AdmissionSourceUnknown, AdmissionListNone, true, AdmissionInferred},
		{AdmissionSourceUnknown, AdmissionListNone, false, AdmissionIgnore},
		{AdmissionSourceUnknown, AdmissionListDeny, true, AdmissionIgnore},
		{AdmissionSourceUnknown, AdmissionListDeny, false, AdmissionIgnore},
	}
	if len(cells) != 4*3*2 {
		t.Fatalf("enumeration covers %d cells, want 24", len(cells))
	}
	if AdmissionRuleCount != 4*3*2 {
		t.Fatalf("AdmissionRuleCount = %d, want 24 (the quoted policy figure must match the table)", AdmissionRuleCount)
	}
	for _, c := range cells {
		got := AdmissionDecide(c.source, c.list, c.mention)
		if got.Verdict != c.expected {
			t.Errorf("table[%s][%s][mention=%t] = %s, want %s", c.source, c.list, c.mention, got.Verdict, c.expected)
		}
		if got.Source != c.source || got.List != c.list || got.Mention != c.mention {
			t.Errorf("decision coordinates drift: got %+v, want source=%s list=%s mention=%t", got, c.source, c.list, c.mention)
		}
		if !strings.Contains(got.Rule, string(c.source)) || !strings.Contains(got.Rule, string(c.list)) {
			t.Errorf("rule label %q must name its cell coordinates", got.Rule)
		}
	}
}

// TestAdmissionDecideDeterministic 钉住纯函数语义：同输入重复判定全等，
// 且与调用顺序无关。
func TestAdmissionDecideDeterministic(t *testing.T) {
	first := AdmissionDecide(AdmissionSourceAgent, AdmissionListNone, true)
	for i := 0; i < 50; i++ {
		again := AdmissionDecide(AdmissionSourceAgent, AdmissionListNone, true)
		if again != first {
			t.Fatalf("verdict drifted on call %d: %+v vs %+v", i, again, first)
		}
	}
	// 越界词表值 fail closed 而不是 panic：判定面绝不能带崩投递泵。
	weird := AdmissionDecide(AdmissionSource("botnet"), AdmissionList("grey"), true)
	if weird.Verdict != AdmissionInferred && weird.Verdict != AdmissionIgnore {
		t.Fatalf("out-of-vocabulary input must fail closed, got %s", weird.Verdict)
	}
}

// TestAdmissionSourceFromIdentity 逐值断言五值身份枚举的折叠与单向章覆盖。
func TestAdmissionSourceFromIdentity(t *testing.T) {
	cases := []struct {
		identity string
		oneWay   bool
		want     AdmissionSource
	}{
		{IdentityHuman, false, AdmissionSourceUser},
		{IdentityHuman, true, AdmissionSourceSystem}, // one-way stamp overrides
		{IdentityMain, false, AdmissionSourceAgent},
		{IdentitySub, false, AdmissionSourceAgent},
		{IdentitySystem, false, AdmissionSourceSystem},
		{IdentityHeartbeat, false, AdmissionSourceSystem},
		{"", false, AdmissionSourceUnknown},
		{"unknown-garbage", false, AdmissionSourceUnknown},
		{"HUMAN", false, AdmissionSourceUser}, // case-insensitive fold
	}
	for _, c := range cases {
		got := AdmissionSourceFromIdentity(c.identity, c.oneWay)
		if got != c.want {
			t.Errorf("AdmissionSourceFromIdentity(%q, oneWay=%t) = %s, want %s", c.identity, c.oneWay, got, c.want)
		}
	}
}

// TestAdmissionClassifyList 名单分类：deny 永远赢，空值/未登记 none。
func TestAdmissionClassifyList(t *testing.T) {
	allow := []string{"sc_a", "sc_b"}
	deny := []string{"sc_b", "sc_c"}
	cases := []struct {
		contact string
		want    AdmissionList
	}{
		{"sc_a", AdmissionListAllow},
		{"sc_b", AdmissionListDeny}, // in both lists → deny wins
		{"sc_c", AdmissionListDeny},
		{"sc_unknown", AdmissionListNone},
		{"", AdmissionListNone},
		{"  sc_a  ", AdmissionListAllow}, // trimmed contact id still matches
	}
	for _, c := range cases {
		got := AdmissionClassifyList(allow, deny, c.contact)
		if got != c.want {
			t.Errorf("AdmissionClassifyList(%q) = %s, want %s", c.contact, got, c.want)
		}
	}
	if AdmissionClassifyList(nil, nil, "sc_x") != AdmissionListNone {
		t.Error("empty lists must classify as none")
	}
}

// TestAdmissionMentioned @提及检测：结构化寻址，两路命中，空名不匹配。
func TestAdmissionMentioned(t *testing.T) {
	cases := []struct {
		body    string
		to      string
		title   string
		want    bool
		comment string
	}{
		{"请 @sc_target 看一下这份报告", "sc_target", "报告会话", true, "contactId 命中"},
		{"@报告会话 的进度如何", "sc_target", "报告会话", true, "标题命中"},
		{"@SC_TARGET 请查收", "sc_target", "", true, "大小写不敏感"},
		{"普通消息正文无寻址", "sc_target", "报告会话", false, "无 @"},
		{"邮件风格 foo@bar.com 不算寻址", "bar.com", "", false, "必须 @ 开头的名字"},
		{"@ 无名提及", "", "", false, "空名不匹配"},
		{"", "sc_target", "", false, "空正文"},
		{"@sc_other 提及了别人", "sc_target", "", false, "提及对象不同"},
	}
	for _, c := range cases {
		got := AdmissionMentioned(c.body, c.to, c.title)
		if got != c.want {
			t.Errorf("AdmissionMentioned(%q, %q, %q) = %t, want %t (%s)", c.body, c.to, c.title, got, c.want, c.comment)
		}
	}
}

// TestIgnoredAdmissionReceiptVocabulary 新终局值的词表卫生。
func TestIgnoredAdmissionReceiptVocabulary(t *testing.T) {
	if !ValidDeliveryReceiptOutcome(ReceiptIgnoredAdmission) {
		t.Fatal("ignored_admission must be a valid receipt outcome — the gate's terminal verdict must be queryable")
	}
	if !DeliveryReceiptSettled(ReceiptIgnoredAdmission) {
		t.Fatal("ignored_admission is terminal — the 585 settled-skip must treat it as already handled")
	}
	if ReceiptNeedsAction(ReceiptIgnoredAdmission) {
		t.Fatal("ignored_admission is the silent tier (任务894): a policy refusal cannot be fixed by resending, so it must not open the force-injection channel")
	}
}
