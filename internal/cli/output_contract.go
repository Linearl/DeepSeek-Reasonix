package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// 任务 229 G1（结构化 verdict CLI 契约）: single home for the machine-facing
// output vocabulary shared by verdict-producing subcommands — a JSON verdict
// envelope plus semantic exit codes. The 229 audit recorded claim-check
// (222), tasklist_append (226), get_session_status (218), event_wait (228)
// and tool-stats (227) each rolling their own output shape; new subcommands
// emit through this contract instead, and existing ones adopt it gradually
// without changing behaviour.
//
// 契约三要素：
//  1. JSON 信封（verdictEnvelope）：schema_version + tool + verdict，领域负载放
//     payload，消费方先判 verdict 再读负载；
//  2. 语义化退出码：0 = 肯定判定（或可报告的非失败结局），1 = 否定判定或执行
//     失败，2 = 用法/输入错误。run 形态命令的结局分类仍以 classifyRunCompletion
//     （run_completion.go）为唯一信源，其 0/1 语义与本表一致；
//  3. verdict 词表：ok / empty / refuted / error（小写）；claim-check 的
//     CONFIRMED 经 verdictExitCode 计入肯定判定，退出行为不变。
const verdictSchemaVersion = 1

// Verdict vocabulary shared by contract-emitting subcommands.
const (
	VerdictOK      = "ok"      // positive/confirmed outcome
	VerdictEmpty   = "empty"   // succeeded, nothing to report
	VerdictRefuted = "refuted" // negative verdict
	VerdictError   = "error"   // execution failure (io/system)
)

// Semantic exit codes shared by contract-emitting subcommands.
const (
	verdictExitConfirmed = 0 // positive verdict / reportable non-failure
	verdictExitRefuted   = 1 // negative verdict / execution failure
	verdictExitUsage     = 2 // usage or input error
)

// verdictExitCode maps a verdict to its contract exit code. CONFIRMED (the
// claim-check verdict spelling) counts as confirmed; anything unrecognised
// is treated as refuted so a future verdict can never silently exit 0.
func verdictExitCode(verdict string) int {
	switch verdict {
	case VerdictOK, VerdictEmpty, "CONFIRMED":
		return verdictExitConfirmed
	default:
		return verdictExitRefuted
	}
}

// verdictEnvelope is the machine-facing output shape: a stable header a
// script can branch on before reading the per-tool payload.
type verdictEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	Tool          string `json:"tool"`
	Verdict       string `json:"verdict"`
	Detail        string `json:"detail,omitempty"`
	Payload       any    `json:"payload,omitempty"`
}

// writeVerdictEnvelope emits the JSON envelope for tool/verdict and returns
// the contract exit code, so a command's output and its exit status stay one
// decision instead of two.
func writeVerdictEnvelope(w io.Writer, tool, verdict, detail string, payload any) (int, error) {
	blob, err := json.MarshalIndent(verdictEnvelope{
		SchemaVersion: verdictSchemaVersion,
		Tool:          tool,
		Verdict:       verdict,
		Detail:        detail,
		Payload:       payload,
	}, "", "  ")
	if err != nil {
		return verdictExitRefuted, err
	}
	if _, err := fmt.Fprintln(w, string(blob)); err != nil {
		return verdictExitRefuted, err
	}
	return verdictExitCode(verdict), nil
}
