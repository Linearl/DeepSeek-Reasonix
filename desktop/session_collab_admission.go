package main

// tl1340：投递泵准入代码门 + 注入决策可观测化。
//
// 职责边界（与 internal/sessioncollab/admission.go 的分工）：
//   - internal 包持有策略表本体（纯函数，单一真相源）与回执词表；
//   - 本文件只做三件事：把一条 MailMessage 解析成表的三个输入维度（来源
//     靠 roster 身份 + 单向章；名单靠 [agent] 配置；@提及靠结构化检测），
//     把判定结果落成 slog 结构化决策记录，以及在判定面开启时跑滞留告警
//     sweep。所有路由行为（explicit 走原路径 / inferred 强制 followup /
//     ignore 终局结算）发生在 runCollabDelivery 的决策点，不在本文件。
//
// 可观测契约（任务书四字段起）：每个决策路径一条
//
//	slog: delivery_decision / decision_reason / session / topic [+msg_id,
//	     source, list, mention, verdict, rule, ...]
//
// decision 词表：injected（steer 注入当轮）/ queued（落队 follow-up）/
// skipped（判据否决或已结算，含 policy_ignore / already_settled /
// refused_hop / refused_provenance / duplicate）/ degraded（steer 降级、
// open_retry_exhausted）/ failed_retrying（非终态重试）/ stale（滞留 WARN）。
// 判定面关闭（开关 OFF）时不产生任何新日志——旧路径逐字节保持。

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

// collabAdmissionGateEnabled resolves the tl1340 gate live per pump pass (the
// task-264 precedent): the gate only re-routes or declines injection and never
// changes consumption rights, so a settings flip applies from the next pass
// without a restart. An unreadable config stays off — fail toward today's
// behavior.
func collabAdmissionGateEnabled() bool {
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	return collabAdmissionGateFromConfig(cfg)
}

// collabAdmissionGateFromConfig is the pure branch (M3 test pattern): nil
// config or the switch off both keep the gate closed.
func collabAdmissionGateFromConfig(cfg *config.Config) bool {
	return cfg != nil && cfg.Agent.ExperimentalCollabAdmissionGate
}

// collabAdmissionLists resolves the recipient-maintained sender rosters. An
// unreadable config yields empty lists — every sender classifies as "none"
// and the gate still applies its source/mention rules.
func collabAdmissionLists() (allow, deny []string) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil
	}
	return collabAdmissionListsFromConfig(cfg)
}

// collabAdmissionListsFromConfig is the pure branch of the roster lookup.
func collabAdmissionListsFromConfig(cfg *config.Config) (allow, deny []string) {
	if cfg == nil {
		return nil, nil
	}
	return cfg.Agent.CollabAdmissionAllow, cfg.Agent.CollabAdmissionDeny
}

// resolveCollabAdmissionSource maps a mail message to its policy source class.
// The roster answers with the sender's task-348 identity; a structural one-way
// stamp overrides to system (the sender declared itself non-conversational).
// A roster miss is unknown — never guessed.
func resolveCollabAdmissionSource(msg sessioncollab.MailMessage, roster map[string]sessioncollab.Identity) sessioncollab.AdmissionSource {
	oneWay := agent.FeedbackInboxOneWay(msg.Body)
	identityType := ""
	if id, ok := roster[msg.From]; ok {
		identityType = id.IdentityType
	}
	return sessioncollab.AdmissionSourceFromIdentity(identityType, oneWay)
}

// resolveCollabAdmissionDecision is the full per-message gate evaluation:
// resolve the three dimensions and walk the table once. Pure with respect to
// the message — the roster/lists snapshots are captured per delivery pass by
// the caller.
func resolveCollabAdmissionDecision(msg sessioncollab.MailMessage, roster map[string]sessioncollab.Identity, allow, deny []string) sessioncollab.AdmissionDecision {
	source := resolveCollabAdmissionSource(msg, roster)
	list := sessioncollab.AdmissionClassifyList(allow, deny, msg.From)
	mentioned := sessioncollab.AdmissionMentioned(msg.Body, msg.To, rosterTitle(roster, msg.To))
	return sessioncollab.AdmissionDecide(source, list, mentioned)
}

// rosterTitle answers the recipient's topic title for @-detection (naming the
// session by its human-visible title is addressing too).
func rosterTitle(roster map[string]sessioncollab.Identity, contactID string) string {
	if id, ok := roster[contactID]; ok {
		return id.Title
	}
	return ""
}

// logDeliveryDecision emits one structured decision record. The four task
// fields always lead so a log reader can grep
// `delivery_decision=skipped session=sc_x`; extra key/values ride after.
func logDeliveryDecision(level slog.Level, decision, reason, session, topic string, kv ...any) {
	args := make([]any, 0, 8+len(kv))
	args = append(args,
		"delivery_decision", decision,
		"decision_reason", reason,
		"session", session,
		"topic", topic,
	)
	args = append(args, kv...)
	slog.Default().Log(context.Background(), level, "session-collab delivery decision", args...)
}

// logAdmissionDecision emits the decision record enriched with the policy
// coordinates (the gate's own trails: who was judged by which cell).
func logAdmissionDecision(level slog.Level, decision, reason, session, topic, msgID string, dec sessioncollab.AdmissionDecision, kv ...any) {
	args := append([]any{
		"msg_id", msgID,
		"admission_verdict", string(dec.Verdict),
		"admission_source", string(dec.Source),
		"admission_list", string(dec.List),
		"admission_mention", dec.Mention,
		"admission_rule", dec.Rule,
		"admission_policy", sessioncollab.AdmissionPolicyVersion,
	}, kv...)
	logDeliveryDecision(level, decision, reason, session, topic, args...)
}

// admissionPolicyReference renders the one-line policy citation prepended to
// the delivery text when the gate is on. It REFERENCES the table (version +
// rule count + this message's verdict) and never re-enumerates the rules —
// the table is the single criterion; the model layer only receives the
// already-decided result.
func admissionPolicyReference(dec sessioncollab.AdmissionDecision) string {
	mention := "否"
	if dec.Mention {
		mention = "是"
	}
	return "[准入] 策略表 " + sessioncollab.AdmissionPolicyVersion +
		"（" + strconv.Itoa(sessioncollab.AdmissionRuleCount) + " 规则）判定=" + string(dec.Verdict) +
		"（来源=" + string(dec.Source) +
		" 名单=" + string(dec.List) +
		" @提及=" + mention +
		"）"
}

// collabAdmissionStalePendingThreshold is the retention-WARN ceiling (④): a
// mail message still pending (claimed every pass by the pump's read, never
// acked = never consumed) after this long is stranded delivery — the WARN
// makes it searchable by session instead of discoverable only by diffing
// mailbox files. Fixed constant, not a config knob: the value bounds a
// diagnostic surface, and a second config key would be a mechanism to
// maintain for a number nobody has asked to tune.
const collabAdmissionStalePendingThreshold = 30 * time.Minute

// sweepStaleMailPending is the pump-side retention sweep (tl1340 ④). "已读
// 未消费" at the mail layer means: the pump's Claim re-reads the message every
// pass (at-least-once delivery contract) while nothing settles it — after the
// threshold it is stranded. One WARN per message per threshold window keeps it
// visible without flooding the log at the 4s tick; entries for messages that
// left the mailbox are dropped so the dedup map cannot grow without bound.
func (p *sessionCollabPump) sweepStaleMailPending(now time.Time) {
	mailDir := config.SessionCollabMailDir()
	if mailDir == "" {
		return
	}
	mail := sessioncollab.NewMailStoreWithHopLimit(mailDir, sessionCollabHopLimit())
	contacts := mail.PendingContacts()
	if len(contacts) == 0 {
		p.staleWarnedMu.Lock()
		p.staleWarned = nil
		p.staleWarnedMu.Unlock()
		return
	}
	roster := p.app.collabRosterIndex()
	stillPending := make(map[string]bool)
	type staleHit struct {
		contact string
		msg     sessioncollab.MailMessage
	}
	var hits []staleHit
	for _, contact := range contacts {
		pending, err := mail.Peek(contact)
		if err != nil {
			continue
		}
		for _, msg := range pending {
			stillPending[msg.ID] = true
			sent := time.UnixMilli(msg.At)
			if now.Sub(sent) < collabAdmissionStalePendingThreshold {
				continue
			}
			p.staleWarnedMu.Lock()
			last, warned := p.staleWarned[msg.ID]
			suppress := warned && now.Sub(last) < collabAdmissionStalePendingThreshold
			if !suppress {
				if p.staleWarned == nil {
					p.staleWarned = map[string]time.Time{}
				}
				p.staleWarned[msg.ID] = now
			}
			p.staleWarnedMu.Unlock()
			if !suppress {
				hits = append(hits, staleHit{contact: contact, msg: msg})
			}
		}
	}
	for _, h := range hits {
		topic := ""
		if id, ok := roster[h.contact]; ok {
			topic = id.TopicID
		}
		age := now.Sub(time.UnixMilli(h.msg.At))
		logDeliveryDecision(slog.LevelWarn, "stale",
			"mail_pending_over_threshold", h.contact, topic,
			"msg_id", h.msg.ID,
			"from", h.msg.From,
			"age_minutes", int(age.Minutes()))
	}
	// The threshold-window dedup means a stranded message re-warns at most
	// once per window — searchable without being a 4s-tick flood.
	p.staleWarnedMu.Lock()
	for id := range p.staleWarned {
		if !stillPending[id] {
			delete(p.staleWarned, id)
		}
	}
	p.staleWarnedMu.Unlock()
}
