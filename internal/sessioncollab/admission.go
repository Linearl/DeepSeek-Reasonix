package sessioncollab

// tl1340：准入矩阵代码化（admission policy table）。
//
// 设计输入（用户 20261103 采纳）：来源 × 名单 × @提及 → explicit/inferred/ignore
// 三值确定性策略表，代码门先于模型语义层。三个不变式：
//
//  1. 单一真相源：这张表是「一条协作消息有没有资格注入目标会话」的唯一判据。
//     prompt/渲染文本只引用判定结果与版本号，绝不重复枚举规则——同一语义
//     两处定义必有一套腐化（防双真相源）。
//  2. 信任边界：消息正文不改变权限。唯一允许读正文的维度是 @提及 的结构化
//     寻址检测（"@<contactId>" / "@<标题>" 字面匹配）——它是寻址信号，不是
//     语义判断；判定永远由纯函数给出，模型层只接收已判定的结果。
//  3. 确定性：同输入同输出。表在编译期写死（24 格字面量），无运行时配置
//     改写表本身；名单（allow/deny）是查表输入，不是表的一部分。
//
// 三值语义（verdict → 泵的行为映射，见 desktop/session_collab_admission.go）：
//   - explicit：高置信注入资格——按消息自身的 delivery 意图走（steer 可尝试
//     注入当轮）。
//   - inferred：照送但强制降级为排队 follow-up——不允许打断目标当轮。
//   - ignore：不注入，终局结算（回执 ignored_admission + WARN 日志），不发
//     送方通知——重试不改变判定（信任边界），通知只会喂养重试循环。
//
// 名单语义：allow/deny 是接受方维护的发送方 contactId 名单（配置面
// [agent] collab_admission_allow / collab_admission_deny，v1 全局共享——
// per-target 名单等真实需求出现再建，避免提前造机制）。deny 优先于 allow
// （同一 contactId 同时命中两名单时按 deny 算——拒绝永远赢）。
//
// 来源语义：发送方的五值身份枚举（task-348）折叠为三源 + unknown：
// human→user，main|sub→agent，system|heartbeat→system，未登记→unknown；
// 结构化单向章（FeedbackInboxOneWay，tl1058）把来源强制为 system——盖章
// 是发送方对「我不是可对话会话」的自我声明，优先于身份枚举。

import (
	"fmt"
	"strings"
)

// AdmissionSource is the sender class a mail message resolves to.
type AdmissionSource string

const (
	AdmissionSourceUser    AdmissionSource = "user"    // human-identity session
	AdmissionSourceAgent   AdmissionSource = "agent"   // main/sub agent session
	AdmissionSourceSystem  AdmissionSource = "system"  // system/heartbeat identity or one-way stamp
	AdmissionSourceUnknown AdmissionSource = "unknown" // not registered / roster miss
)

// AdmissionList is the recipient-maintained roster bucket for a sender.
type AdmissionList string

const (
	AdmissionListAllow AdmissionList = "allow"
	AdmissionListDeny  AdmissionList = "deny"
	AdmissionListNone  AdmissionList = "none"
)

// AdmissionVerdict is the three-value policy outcome.
type AdmissionVerdict string

const (
	// AdmissionExplicit — high-confidence injection eligibility: the message
	// may go out with its own delivery intent (steer allowed).
	AdmissionExplicit AdmissionVerdict = "explicit"
	// AdmissionInferred — deliverable, but forced down to a queued follow-up.
	AdmissionInferred AdmissionVerdict = "inferred"
	// AdmissionIgnore — not injected; terminal settle with a WARN trail.
	AdmissionIgnore AdmissionVerdict = "ignore"
)

// AdmissionPolicyVersion names the table version for logs and the delivery
// text's one-line policy reference ("准入策略=v1 显式规则 N 条"). Bump when the
// table contents change shape so a log reader can tell which rules produced a
// historical verdict.
const AdmissionPolicyVersion = "v1"

// AdmissionMentioned reports whether the message explicitly @-addresses the
// recipient. Detection is purely structural: the body contains
// "@<recipient contactId>" or "@<recipient topic title>" (case-insensitive)
// at a token boundary — an occurrence inside a larger word (the email address
// "foo@bar.com", the contact "sc_target_extra" when looking for "sc_target")
// is not an address. RequireReply is deliberately NOT a mention — a reply
// obligation is not an address. An empty recipient name can never match (an
// "@" alone is noise).
func AdmissionMentioned(body, toContactID, toTitle string) bool {
	lower := strings.ToLower(body)
	if lower == "" {
		return false
	}
	for _, name := range []string{toContactID, toTitle} {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if containsTokenMention(lower, name) {
			return true
		}
	}
	return false
}

// containsTokenMention finds "@<name>" at token boundaries: the byte before
// '@' must not be an ASCII letter/digit (it may be a space, CJK rune,
// punctuation or string start), and the byte after the name must not be a
// contactID character (letter/digit/underscore/hyphen). Both directions are
// needed: "foo@bar.com" must not address bar.com, and "@sc_target" inside
// "@sc_target_extra" must not address sc_target.
func containsTokenMention(lowerBody, name string) bool {
	needle := "@" + name
	for from := 0; ; {
		idx := strings.Index(lowerBody[from:], needle)
		if idx < 0 {
			return false
		}
		idx += from
		beforeOK := idx == 0 || !isAdmissionWordByte(lowerBody[idx-1])
		after := idx + len(needle)
		afterOK := after >= len(lowerBody) || !isContactIDByte(lowerBody[after])
		if beforeOK && afterOK {
			return true
		}
		from = idx + 1
	}
}

// isAdmissionWordByte: bytes that make a preceding '@' mid-word.
func isAdmissionWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

// isContactIDByte: bytes that extend a name past its end.
func isContactIDByte(b byte) bool {
	return isAdmissionWordByte(b) || b == '_' || b == '-'
}

// AdmissionClassifyList resolves a sender's roster bucket. Deny wins over
// allow; an unknown sender is "none" (an empty allow/deny entry is ignored).
func AdmissionClassifyList(allow, deny []string, contactID string) AdmissionList {
	contactID = strings.TrimSpace(contactID)
	if contactID == "" {
		return AdmissionListNone
	}
	for _, id := range deny {
		if strings.TrimSpace(id) == contactID {
			return AdmissionListDeny
		}
	}
	for _, id := range allow {
		if strings.TrimSpace(id) == contactID {
			return AdmissionListAllow
		}
	}
	return AdmissionListNone
}

// AdmissionSourceFromIdentity folds the five-value identity enum (task-348)
// into the three source classes. An empty or unrecognized identity is
// unknown — the caller never guesses. oneWay (the structural one-way stamp)
// overrides to system regardless of the registered identity.
func AdmissionSourceFromIdentity(identityType string, oneWay bool) AdmissionSource {
	if oneWay {
		return AdmissionSourceSystem
	}
	switch strings.ToLower(strings.TrimSpace(identityType)) {
	case IdentityHuman:
		return AdmissionSourceUser
	case IdentityMain, IdentitySub:
		return AdmissionSourceAgent
	case IdentitySystem, IdentityHeartbeat:
		return AdmissionSourceSystem
	default:
		return AdmissionSourceUnknown
	}
}

// admissionTable is THE single source of truth: verdict for every
// source × list × mention combination, written out as 24 literal cells so a
// reviewer sees the whole policy at the definition site. Cell order:
// [source][list][mentioned]. A changing cell must bump AdmissionPolicyVersion.
//
// Rationale per row (kept here, not in any prompt):
//   - user: a human sender is never ignored on roster-less addressing —
//     named (@ or allow) → explicit, broadcast → inferred; deny → ignore
//     (the recipient explicitly excluded that contact).
//   - agent: same shape as user — named → explicit, broadcast → inferred,
//     deny → ignore.
//   - system: allow-listed system senders are explicit; anything else is
//     inferred at best — an unnamed system broadcast (feedback-inbox style)
//     is ignore: it was never addressable to this session (tl1058 spirit:
//     one-way notices must not enter the model context uninvited).
//   - unknown: allow-listed senders are explicit (the list is contact-level
//     trust, independent of identity registration); fail closed otherwise —
//     an unknown source with no @ and no allow entry is ignore.
var admissionTable = [4][3][2]AdmissionVerdict{
	// AdmissionSourceUser
	{
		// allow: {mentioned, notMentioned}
		{AdmissionExplicit, AdmissionExplicit},
		// none
		{AdmissionExplicit, AdmissionInferred},
		// deny
		{AdmissionIgnore, AdmissionIgnore},
	},
	// AdmissionSourceAgent
	{
		{AdmissionExplicit, AdmissionExplicit},
		{AdmissionExplicit, AdmissionInferred},
		{AdmissionIgnore, AdmissionIgnore},
	},
	// AdmissionSourceSystem
	{
		{AdmissionExplicit, AdmissionExplicit},
		{AdmissionInferred, AdmissionIgnore},
		{AdmissionIgnore, AdmissionIgnore},
	},
	// AdmissionSourceUnknown
	{
		{AdmissionExplicit, AdmissionExplicit},
		{AdmissionInferred, AdmissionIgnore},
		{AdmissionIgnore, AdmissionIgnore},
	},
}

// admissionSources / admissionLists index the table dimensions. The enums are
// deliberately ordered to match the array above; the mapping functions are
// the only place that knows the ordinal, so reordering the constants cannot
// silently scramble the table.
func admissionSourceIndex(s AdmissionSource) (int, error) {
	switch s {
	case AdmissionSourceUser:
		return 0, nil
	case AdmissionSourceAgent:
		return 1, nil
	case AdmissionSourceSystem:
		return 2, nil
	case AdmissionSourceUnknown:
		return 3, nil
	default:
		return 0, fmt.Errorf("sessioncollab: unknown admission source %q", string(s))
	}
}

func admissionListIndex(l AdmissionList) (int, error) {
	switch l {
	case AdmissionListAllow:
		return 0, nil
	case AdmissionListNone:
		return 1, nil
	case AdmissionListDeny:
		return 2, nil
	default:
		return 0, fmt.Errorf("sessioncollab: unknown admission list %q", string(l))
	}
}

// AdmissionDecision is one lookup's full result: the verdict plus the cell
// coordinates that produced it, so logs can name the exact rule.
type AdmissionDecision struct {
	Verdict AdmissionVerdict
	Source  AdmissionSource
	List    AdmissionList
	Mention bool
	// Rule names the matched cell as "source|list|mention" — the table's
	// coordinate, not a prose paraphrase.
	Rule string
}

// AdmissionDecide is the pure policy lookup. It never reads message content:
// the caller resolves source/list/mention through the helpers above and this
// function only walks the table.
func AdmissionDecide(source AdmissionSource, list AdmissionList, mentioned bool) AdmissionDecision {
	si, err := admissionSourceIndex(source)
	if err != nil {
		// Defensive: an out-of-vocabulary source fails closed to unknown's row
		// rather than panicking — the gate must never take the pump down.
		source = AdmissionSourceUnknown
		si, _ = admissionSourceIndex(source)
	}
	li, err := admissionListIndex(list)
	if err != nil {
		list = AdmissionListNone
		li, _ = admissionListIndex(list)
	}
	mentionIdx := 0 // mentioned
	if !mentioned {
		mentionIdx = 1
	}
	rule := fmt.Sprintf("%s|%s|mention=%t", source, list, mentioned)
	return AdmissionDecision{
		Verdict: admissionTable[si][li][mentionIdx],
		Source:  source,
		List:    list,
		Mention: mentioned,
		Rule:    rule,
	}
}

// AdmissionRuleCount is the table's cell count — the "N 条显式规则" figure the
// delivery text's policy reference line quotes. Kept in sync with the array
// by admission_test.go (it enumerates every cell), so the quoted number can
// never drift from the real table.
const AdmissionRuleCount = 4 * 3 * 2
