import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ChevronDown, ChevronRight, Clipboard, Loader2, RefreshCw } from "lucide-react";
import { app } from "../lib/bridge";
import { asArray } from "../lib/array";
import { useI18n, useT, type DictKey, type Locale, type Translator } from "../lib/i18n";
import type { CapabilityDiagnosticsReport, CapabilityIssue, CrashPendingDiagnosticsReport, RuntimeDoctorReport, SettingsTab } from "../lib/types";
import { FrontendDiagnosticsControl } from "./FrontendDiagnosticsControl";

const FRONTEND_COPY: Record<Locale, { title: string; hint: string }> = {
  en: {
    title: "Frontend interaction recording",
    hint: "When scrolling jumps, sessions switch, or the UI flickers, turn this on, reproduce the issue, then turn it off and choose where to export. Only timing, events, and geometry are recorded; conversation content, input values, paths, and secrets are excluded.",
  },
  zh: {
    title: "前端交互记录",
    hint: "遇到滚动闪回、会话切换或界面抖动时，打开记录开关，复现问题后关闭并选择路径导出。仅记录时间、事件和几何信息，不记录对话内容、输入值、路径或密钥。",
  },
  "zh-TW": {
    title: "前端互動記錄",
    hint: "遇到捲動閃回、工作階段切換或介面抖動時，開啟記錄開關，重現問題後關閉並選擇路徑匯出。僅記錄時間、事件與幾何資訊，不記錄對話內容、輸入值、路徑或密鑰。",
  },
};

export function DiagnosticsSettingsPage({
  onNavigate,
}: {
  onNavigate?: (tab: SettingsTab) => void;
}) {
  const t = useT();
  const { locale } = useI18n();
  const frontendCopy = FRONTEND_COPY[locale];
  const [report, setReport] = useState<CapabilityDiagnosticsReport | null>(null);
  const [runtimeDoctor, setRuntimeDoctor] = useState<RuntimeDoctorReport | null>(null);
  const [crashPending, setCrashPending] = useState<CrashPendingDiagnosticsReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [includeRuntime, setIncludeRuntime] = useState(false);
  const [copied, setCopied] = useState(false);
  const [open, setOpen] = useState<Record<string, boolean>>({
    issues: true,
    runtime: true,
    instructions: false,
    skills: false,
    commands: false,
    hooks: false,
    plugins: false,
    mcp: false,
  });

  const loadSeq = useRef(0);

  const load = useCallback(async (runtime: boolean) => {
    const seq = ++loadSeq.current;
    setLoading(true);
    setError(null);
    try {
      const next = normalizeDiagnosticsReport(await app.CapabilityDiagnostics(runtime));
      let doctor: RuntimeDoctorReport | null = null;
      try {
        doctor = await app.RuntimeDoctor();
      } catch {
        doctor = null;
      }
      // Task 618: local crash-pending queue state. Kept non-fatal — an older
      // backend without the binding must not break the whole page.
      let pending: CrashPendingDiagnosticsReport | null = null;
      try {
        pending = await app.CrashPendingDiagnostics();
      } catch {
        pending = null;
      }
      // Last-request-wins: ignore stale responses after rapid refresh/toggle.
      if (seq !== loadSeq.current) return;
      setReport(next);
      setRuntimeDoctor(doctor);
      setCrashPending(pending);
    } catch (err) {
      if (seq !== loadSeq.current) return;
      setError(err instanceof Error ? err.message : String(err));
      setReport(null);
      setRuntimeDoctor(null);
      setCrashPending(null);
    } finally {
      if (seq === loadSeq.current) {
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    void load(includeRuntime);
  }, [includeRuntime, load]);

  const issuesBySeverity = useMemo(() => {
    const groups: Record<string, CapabilityIssue[]> = { error: [], warning: [], info: [] };
    for (const issue of report?.issues ?? []) {
      const key = issue.severity === "error" || issue.severity === "warning" ? issue.severity : "info";
      groups[key].push(issue);
    }
    return groups;
  }, [report]);

  const copyJSON = async () => {
    if (!report) return;
    try {
      await navigator.clipboard.writeText(JSON.stringify(report, null, 2));
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      setError(t("diag.copyFailed"));
    }
  };

  const toggle = (key: string) => setOpen((prev) => ({ ...prev, [key]: !prev[key] }));

  const goSettings = (tab?: string) => {
    if (!tab || !onNavigate) return;
    const allowed: SettingsTab[] = ["mcp", "skills", "plugins", "hooks"];
    if (allowed.includes(tab as SettingsTab)) {
      onNavigate(tab as SettingsTab);
    }
  };

  return (
    <div className="diag-page">
      <div className="diag-page__toolbar settings-toolbar">
        <label className="diag-page__runtime">
          <input
            type="checkbox"
            checked={includeRuntime}
            onChange={(e) => setIncludeRuntime(e.target.checked)}
          />
          <span>{t("diag.includeRuntime")}</span>
        </label>
        <div className="diag-page__actions">
          <button type="button" className="btn btn--secondary" onClick={() => void load(includeRuntime)} disabled={loading}>
            {loading ? <Loader2 size={14} className="spin" /> : <RefreshCw size={14} />}
            <span>{t("diag.refresh")}</span>
          </button>
          <button type="button" className="btn btn--secondary" onClick={() => void copyJSON()} disabled={!report}>
            <Clipboard size={14} />
            <span>{copied ? t("diag.copied") : t("diag.copyJson")}</span>
          </button>
        </div>
      </div>

      <p className="diag-page__hint">{t("diag.hint")}</p>

      {crashPending && (
        <section className="diag-section" data-testid="crash-pending-diagnostics">
          <div className="diag-section__body">
            <div className="diag-frontend-recording__copy">
              <strong>{t("diag.crashPending.title")}</strong>
              {crashPending.count === 0 ? (
                <span>{t("diag.crashPending.empty")}</span>
              ) : (
                <span>
                  {t("diag.crashPending.summary", {
                    count: crashPending.count,
                    capacity: crashPending.capacity,
                    retention: crashPending.retentionDays,
                  })}
                  {crashPending.oldestAt
                    ? ` · ${t("diag.crashPending.range", {
                        oldest: formatCrashPendingTime(crashPending.oldestAt, locale),
                        newest: formatCrashPendingTime(crashPending.newestAt ?? crashPending.oldestAt, locale),
                      })}`
                    : ""}
                </span>
              )}
            </div>
            {crashPending.atCapacity && (
              <p className="settings-error" role="alert">
                {t("diag.crashPending.atCapacity", { count: crashPending.count, capacity: crashPending.capacity })}
              </p>
            )}
          </div>
        </section>
      )}

      <section className="diag-section diag-section--frontend" data-testid="frontend-diagnostics-settings">
        <div className="diag-section__body diag-section__body--frontend">
          <div className="diag-frontend-recording__copy">
            <strong>{frontendCopy.title}</strong>
            <span>{frontendCopy.hint}</span>
          </div>
          <FrontendDiagnosticsControl embedded />
        </div>
      </section>

      {loading && !report && <div className="empty">{t("settings.loading")}</div>}
      {error && <div className="settings-error" role="alert">{error}</div>}

      {report && (
        <>
          <div className="diag-summary">
            <div className="diag-summary__item diag-summary__item--error">
              <strong>{report.summary.errors}</strong>
              <span>{t("diag.errors")}</span>
            </div>
            <div className="diag-summary__item diag-summary__item--warning">
              <strong>{report.summary.warnings}</strong>
              <span>{t("diag.warnings")}</span>
            </div>
            <div className="diag-summary__item diag-summary__item--info">
              <strong>{report.summary.infos}</strong>
              <span>{t("diag.infos")}</span>
            </div>
            <div className="diag-summary__meta">
              <span className="diag-path">{report.root}</span>
              <span>
                {t("diag.counts", {
                  skills: report.summary.skills,
                  commands: report.summary.commands,
                  hooks: report.summary.hooks,
                  plugins: report.summary.plugins,
                  mcp: report.summary.mcp_servers,
                })}
              </span>
            </div>
          </div>

          {runtimeDoctor && (
            <section className="diag-section">
              <button type="button" className="diag-section__header" onClick={() => toggle("runtime")}>
                {open.runtime ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                <span>{t("diag.runtime.title")}</span>
              </button>
              {open.runtime && (
                <div className="diag-section__body">
                  <div className="diag-summary">
                    <div className="diag-summary__item">
                      <strong>{runtimeDoctor.publishedGeneration}</strong>
                      <span>{t("diag.runtime.generation")}</span>
                    </div>
                    <div className="diag-summary__item">
                      <strong>{runtimeDoctor.allowResume ? t("diag.yes") : t("diag.no")}</strong>
                      <span>{t("diag.runtime.allowResume")}</span>
                    </div>
                    <div className="diag-summary__item">
                      <strong>{runtimeDoctor.cleanRollback ? t("diag.yes") : t("diag.no")}</strong>
                      <span>{t("diag.runtime.cleanRollback")}</span>
                    </div>
                    <div className="diag-summary__meta">
                      <span>
                        {t("diag.runtime.metric.noOp")}={runtimeDoctor.noOpRebuilds} {t("diag.runtime.metric.subgraph")}={runtimeDoctor.subgraphRebuilds} {t("diag.runtime.metric.full")}={runtimeDoctor.fullRebuilds}{" "}
                        {t("diag.runtime.metric.staleDrops")}={runtimeDoctor.staleDrops} {t("diag.runtime.metric.admitReject")}={runtimeDoctor.admissionRejected} {t("diag.runtime.metric.ownerFallbacks")}={runtimeDoctor.runtimeOwnerFallbacks}
                      </span>
                    </div>
                  </div>
                  <p className="diag-page__hint">{t("diag.runtime.rawNote")}</p>
                  <pre className="diag-path" style={{ whiteSpace: "pre-wrap", marginTop: 8 }}>
                    {runtimeDoctor.text}
                  </pre>
                </div>
              )}
            </section>
          )}

          <section className="diag-section">
            <button type="button" className="diag-section__header" onClick={() => toggle("issues")}>
              {open.issues ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
              <span>{t("diag.issues")} ({report.issues.length})</span>
            </button>
            {open.issues && (
              <div className="diag-section__body">
                {report.issues.length === 0 && <div className="empty">{t("diag.noIssues")}</div>}
                {(["error", "warning", "info"] as const).map((sev) =>
                  issuesBySeverity[sev].length === 0 ? null : (
                    <div key={sev} className={`diag-issue-group diag-issue-group--${sev}`}>
                      <h4>{t(`diag.severity.${sev}` as "diag.severity.error")}</h4>
                  {issuesBySeverity[sev].map((issue, idx) => {
                    const copy = localizeIssue(issue, t);
                    return (
                      <article key={`${issue.code}-${issue.name ?? ""}-${idx}`} className="diag-issue">
                        <header>
                          <code>{issue.code}</code>
                          {issue.name ? <span className="diag-issue__name">{issue.name}</span> : null}
                        </header>
                        <p className="diag-issue__msg">{copy.message}</p>
                        {issue.source ? <p className="diag-path">{issue.source}</p> : null}
                        {copy.remediation ? <p className="diag-issue__fix">{copy.remediation}</p> : null}
                        {issue.settings_tab && onNavigate ? (
                          <button type="button" className="btn btn--secondary btn--small" onClick={() => goSettings(issue.settings_tab)}>
                            {t("diag.gotoSettings")}
                          </button>
                        ) : null}
                      </article>
                    );
                  })}
                    </div>
                  ),
                )}
              </div>
            )}
          </section>

          <Collapsible
            title={t("diag.instructions")}
            count={report.instructions.docs.length}
            open={!!open.instructions}
            onToggle={() => toggle("instructions")}
          >
            {report.instructions.docs.map((d) => (
              <div key={`${d.order}-${d.path}`} className="diag-row">
                <span>{d.order}. [{d.scope} · {d.depth}]</span>
                <span className="diag-path">{d.path}</span>
              </div>
            ))}
            {report.instructions.docs.length === 0 && <div className="empty">{t("common.none")}</div>}
          </Collapsible>

          <Collapsible
            title={t("diag.skills")}
            count={report.skills.winners}
            open={!!open.skills}
            onToggle={() => toggle("skills")}
          >
            {report.skills.entries.filter((e) => e.status === "winner").map((e) => (
              <div key={`${e.name}-${e.path}`} className="diag-row">
                <span>{e.name}</span>
                <span className="diag-path">{e.path}</span>
              </div>
            ))}
          </Collapsible>

          <Collapsible
            title={t("diag.commands")}
            count={report.commands.winners}
            open={!!open.commands}
            onToggle={() => toggle("commands")}
          >
            {report.commands.entries.filter((e) => e.status === "winner").map((e) => (
              <div key={`${e.name}-${e.path}`} className="diag-row">
                <span>/{e.name}</span>
                <span className="diag-path">{e.path}</span>
              </div>
            ))}
          </Collapsible>

          <Collapsible
            title={t("diag.hooks")}
            count={report.hooks.entries.length}
            open={!!open.hooks}
            onToggle={() => toggle("hooks")}
          >
            {report.hooks.entries.map((e, i) => (
              <div key={`${e.event}-${e.source}-${i}`} className="diag-row">
                <span>{e.event} [{e.scope}]</span>
                <span className="diag-path">{e.source}</span>
              </div>
            ))}
          </Collapsible>

          <Collapsible
            title={t("diag.plugins")}
            count={report.plugins.packages.length}
            open={!!open.plugins}
            onToggle={() => toggle("plugins")}
          >
            {report.plugins.packages.map((p) => (
              <div key={p.name} className="diag-row">
                <span>{p.name} ({p.status})</span>
                <span className="diag-path">{p.root}</span>
              </div>
            ))}
          </Collapsible>

          <Collapsible
            title={t("diag.mcp")}
            count={report.mcp.servers.length}
            open={!!open.mcp}
            onToggle={() => toggle("mcp")}
          >
            {report.mcp.servers.map((s) => (
              <div key={s.name} className="diag-row">
                <span>
                  {s.name} · {s.transport} · {s.start_intent}
                  {s.runtime_status ? ` · ${s.runtime_status}` : ""}
                </span>
                <span className="diag-path">{s.source || s.command || s.url_host || ""}</span>
              </div>
            ))}
          </Collapsible>
        </>
      )}
    </div>
  );
}

// Task 624: localize capability-issue sentences by issue code (option B — the
// code itself stays English for search/upstream parity). When the backend
// message matches the known template (regex with named groups), it renders from
// locale keys; {detail} keeps the verbatim technical tail (paths, sanitized
// errors). Arbitrary backend texts (instruction.* notes, matcher errors, plugin
// compatibility warnings, sanitized stderr) have no stable template: the
// message stays verbatim as a technical value, while their fixed remediation
// still localizes. Unknown codes fall back to the original backend text.
type IssueCopy = {
  msg?: DictKey;
  rem?: DictKey;
  re?: RegExp;
  // English reason suffix → locale key, for the shared allowed-tools template.
  reason?: Record<string, DictKey>;
};

const TOOL_REF_RE = /^skill "(?<skill>[^"]+)" allowed-tools reference "(?<ref>[^"]*)" (?<reason>.+)$/;

const TOOL_REF_REASONS: Record<string, DictKey> = {
  "has invalid glob syntax": "diag.issue.skill.toolRef.reason.glob",
  "has an incomplete or invalid MCP reference": "diag.issue.skill.toolRef.reason.mcpRef",
  "matches multiple MCP tools; use a qualified reference": "diag.issue.skill.toolRef.reason.ambiguous",
  "is unverified by the offline inventory; resolve it in the target session": "diag.issue.skill.toolRef.reason.unverified",
  "is not a known tool identity": "diag.issue.skill.toolRef.reason.unknown",
};

const toolRefCopy: IssueCopy = { msg: "diag.issue.skill.toolRef.msg", rem: "diag.issue.skill.toolRef.rem", re: TOOL_REF_RE, reason: TOOL_REF_REASONS };

const ISSUE_COPY: Record<string, IssueCopy> = {
  "config.load_failed": {
    msg: "diag.issue.config.load_failed.msg", rem: "diag.issue.config.load_failed.rem",
    re: /^failed to load configuration: (?<detail>.+)$/s,
  },
  "mcp.runtime_unavailable": { msg: "diag.issue.mcp.runtime_unavailable.msg", rem: "diag.issue.mcp.runtime_unavailable.rem" },
  "instruction.placeholder": { rem: "diag.issue.instruction.rem" }, // dynamic codes: instruction.<diagnostic.Code>
  "skill.missing_description": { msg: "diag.issue.skill.missing_description.msg", rem: "diag.issue.skill.missing_description.rem" },
  "skill.shadowed": {
    msg: "diag.issue.skill.shadowed.msg", rem: "diag.issue.skill.shadowed.rem",
    re: /^skill is shadowed by a higher-priority winner at (?<detail>.+)$/,
  },
  "skill.disabled": { msg: "diag.issue.skill.disabled.msg", rem: "diag.issue.skill.disabled.rem" },
  "skill.mcp_dependency_missing": {
    msg: "diag.issue.skill.mcp_dependency_missing.msg", rem: toolRefCopy.rem,
    re: /^skill "(?<skill>[^"]+)" requires (?<dep>\S+) but that MCP server is not configured$/,
  },
  "skill.mcp_dependency_failed": {
    msg: "diag.issue.skill.mcp_dependency_failed.msg", rem: toolRefCopy.rem,
    re: /^skill "(?<skill>[^"]+)" requires (?<dep>\S+) which is host-failed: (?<detail>.+)$/s,
  },
  "skill.tool_reference_invalid": toolRefCopy,
  "skill.tool_reference_ambiguous": toolRefCopy,
  "skill.tool_reference_unverified": toolRefCopy,
  "skill.tool_reference_unknown": toolRefCopy,
  "command.shadowed": {
    msg: "diag.issue.command.shadowed.msg", rem: "diag.issue.command.shadowed.rem",
    re: /^command is overridden by later directory winner at (?<detail>.+)$/,
  },
  "command.read_failed": { msg: "diag.issue.command.read_failed.msg", rem: "diag.issue.command.read_failed.rem" },
  "hook.malformed_settings": { msg: "diag.issue.hook.malformed_settings.msg", rem: "diag.issue.hook.malformed_settings.rem" },
  "hook.missing_command": { msg: "diag.issue.hook.missing_command.msg", rem: "diag.issue.hook.missing_command.rem" },
  "hook.missing_context_file": { msg: "diag.issue.hook.missing_context_file.msg", rem: "diag.issue.hook.missing_context_file.rem" },
  "hook.invalid_matcher": { rem: "diag.issue.hook.invalid_matcher.rem" },
  "hook.unknown_event": { msg: "diag.issue.hook.unknown_event.msg", rem: "diag.issue.hook.unknown_event.rem" },
  "hook.shell_unavailable": { rem: "diag.issue.hook.shell_unavailable.rem" },
  "plugin.state_read_failed": { msg: "diag.issue.plugin.state_read_failed.msg", rem: "diag.issue.plugin.state_read_failed.rem" },
  "plugin.missing_root": { msg: "diag.issue.plugin.missing_root.msg", rem: "diag.issue.plugin.missing_root.rem" },
  "plugin.invalid_manifest": {
    msg: "diag.issue.plugin.invalid_manifest.msg", rem: "diag.issue.plugin.invalid_manifest.rem",
    re: /^plugin package manifest is invalid: (?<detail>.+)$/s,
  },
  "plugin.compatibility": { rem: "diag.issue.plugin.compatibility.rem" },
  "mcp.invalid_transport": {
    msg: "diag.issue.mcp.invalid_transport.msg", rem: "diag.issue.mcp.invalid_transport.rem",
    re: /^unsupported MCP transport (?<detail>.+)$/,
  },
  "mcp.missing_command": { msg: "diag.issue.mcp.missing_command.msg", rem: "diag.issue.mcp.missing_command.rem" },
  "mcp.command_not_found": { msg: "diag.issue.mcp.command_not_found.msg", rem: "diag.issue.mcp.command_not_found.rem" },
  "mcp.missing_url": { msg: "diag.issue.mcp.missing_url.msg", rem: "diag.issue.mcp.missing_url.rem" },
  "mcp.no_tools": { msg: "diag.issue.mcp.no_tools.msg", rem: "diag.issue.mcp.no_tools.rem" },
  "mcp.start_failed": {
    msg: "diag.issue.mcp.start_failed.msg", rem: "diag.issue.mcp.start_failed.rem",
    re: /^MCP server failed in the current session: (?<detail>.+)$/s,
  },
};

function localizeIssue(issue: CapabilityIssue, t: Translator): { message: string; remediation?: string } {
  const copy = ISSUE_COPY[issue.code] ?? (issue.code.startsWith("instruction.") ? ISSUE_COPY["instruction.placeholder"] : undefined);
  let message = issue.message;
  if (copy?.msg) {
    let localized: string | null = null;
    if (!copy.re) {
      localized = t(copy.msg);
    } else {
      const m = copy.re.exec(issue.message);
      if (m?.groups) {
        if (copy.reason) {
          const reasonKey = copy.reason[m.groups.reason ?? ""];
          if (reasonKey) localized = t(copy.msg, { ...m.groups, reason: t(reasonKey) });
        } else {
          localized = t(copy.msg, m.groups);
        }
      }
    }
    if (localized !== null) message = localized;
  }
  const remediation = copy?.rem ? t(copy.rem) : issue.remediation;
  return { message, remediation };
}

function formatCrashPendingTime(iso: string, locale: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  try {
    return at.toLocaleString(locale);
  } catch {
    return at.toISOString();
  }
}

function normalizeDiagnosticsReport(report: CapabilityDiagnosticsReport): CapabilityDiagnosticsReport {
  return {
    ...report,
    issues: asArray(report.issues),
    instructions: { ...report.instructions, docs: asArray(report.instructions?.docs) },
    skills: {
      ...report.skills,
      roots: asArray(report.skills?.roots),
      entries: asArray(report.skills?.entries),
    },
    commands: {
      ...report.commands,
      roots: asArray(report.commands?.roots),
      entries: asArray(report.commands?.entries),
    },
    hooks: {
      ...report.hooks,
      sources: asArray(report.hooks?.sources),
      entries: asArray(report.hooks?.entries),
    },
    plugins: { ...report.plugins, packages: asArray(report.plugins?.packages) },
    mcp: { ...report.mcp, servers: asArray(report.mcp?.servers) },
  };
}

function Collapsible({
  title,
  count,
  open,
  onToggle,
  children,
}: {
  title: string;
  count: number;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
}) {
  return (
    <section className="diag-section">
      <button type="button" className="diag-section__header" onClick={onToggle}>
        {open ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
        <span>
          {title} ({count})
        </span>
      </button>
      {open && <div className="diag-section__body">{children}</div>}
    </section>
  );
}
