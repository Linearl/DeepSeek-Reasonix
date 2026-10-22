import { useCallback, useEffect, useState } from "react";

import {
  app,
  type RecoveryChainSet,
  type RecoveryChainView,
  type SessionHealthReportView,
  type SessionRepairRecordView,
  type SessionVersionGroupView,
} from "../lib/bridge";
import { useT } from "../lib/i18n";

/** 任务 1011 需求 E：设置页「工作阶段版本与修复」分区。
 *
 *  与旧版「恢复副本」面板（legacy session-recovery-<hex>.jsonl 扫描）分区并存：
 *  这里读的是 v3 events 日志层的 heads——本次 ai-passport 异常里旧面板是死路
 *  （v3_only 存储没有 manifest 也没有 recovery-v1.bolt），head 读数才是事实源。
 *  列表零重放（meta.HeadCount 过滤）；展开单组才重放该会话（ListRecoveryChains）；
 *  选择/切换走版本面板同一条 SetActiveSessionVersion 通路（可撤销）；
 *  修复（检查→读数→确认→执行）与控制层自愈共用备份与修复记录。
 */

function formatWhen(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

type Expanded = { path: string; loading: boolean };

/** degradedChainSet is the local stand-in for a conversation whose chain
 *  enumeration was refused (任务 1039r). The backend's own degradation (1019/1039)
 *  turns a replay refusal into a Degraded row, but a rejection that still escapes
 *  it - a directory read error, a non-refusal replay failure - used to leave the
 *  expanded area an empty table with a bare error line. Rendering one zero-count
 *  degraded row keeps the same face as the backend degradation: the row exists,
 *  carries no counts, and is never the recommended chain.
 */
function degradedChainSet(mainPath: string): RecoveryChainSet {
  const row: RecoveryChainView = {
    path: mainPath,
    headId: "",
    label: "",
    isMain: true,
    selected: false,
    covered: false,
    messageCount: 0,
    turns: 0,
    lastActivity: "",
    bytes: 0,
    preview: "",
    degraded: true,
  };
  return { mainPath, mainLabel: "", chains: [row], longestPath: "", longestHead: "" };
}

export function SessionVersionsSection() {
  const t = useT();
  const [groups, setGroups] = useState<SessionVersionGroupView[]>([]);
  const [expanded, setExpanded] = useState<Expanded | null>(null);
  const [chains, setChains] = useState<{ chains: RecoveryChainSet | null }>({ chains: null });
  const [health, setHealth] = useState<{ path: string; report: SessionHealthReportView | null }>({ path: "", report: null });
  const [repairing, setRepairing] = useState(false);
  const [repairNote, setRepairNote] = useState("");
  const [records, setRecords] = useState<SessionRepairRecordView[]>([]);
  const [error, setError] = useState("");

  const reload = useCallback(async () => {
    try {
      setGroups(await app.ListSessionVersionGroups());
    } catch (e) {
      setError(String(e));
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const toggleGroup = async (path: string) => {
    if (expanded?.path === path) {
      setExpanded(null);
      setChains({ chains: null });
      setHealth({ path: "", report: null });
      setRecords([]);
      return;
    }
    setExpanded({ path, loading: true });
    setHealth({ path, report: null });
    setRepairNote("");
    try {
      const [set, recs] = await Promise.all([
        app.ListRecoveryChains(path),
        app.ListSessionRepairRecordsFor(path).catch(() => [] as SessionRepairRecordView[]),
      ]);
      setChains({ chains: set });
      setRecords(recs);
    } catch (e) {
      // 任务 1039r：枚举被拒不再渲染空表——与后端降级同语义的一面 Degraded 行
      // + 原因横幅。失败是局部的：列表与其他分区原样保留，整页错误边界永不参与。
      setChains({ chains: degradedChainSet(path) });
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setExpanded({ path, loading: false });
    }
  };

  const inspect = async (path: string) => {
    try {
      const report = await app.InspectSessionHealthFor(path);
      setHealth({ path, report });
    } catch (e) {
      setError(String(e));
    }
  };

  const repair = async (path: string, diet: boolean) => {
    setRepairing(true);
    setRepairNote("");
    try {
      const report = await app.RepairSessionFor(path, true, true, diet);
      if (!report.repaired) {
        setRepairNote(t("settings.sessionVersions.noRepairNeeded"));
      } else {
        setRepairNote(
          t("settings.sessionVersions.repaired")
            .replace("{actions}", report.actions.map((a) => a.action).join("+"))
            .replace("{backup}", report.backupDir || "—"),
        );
        void reload();
      }
      const fresh = await app.InspectSessionHealthFor(path).catch(() => null);
      if (fresh) setHealth({ path, report: fresh });
    } catch (e) {
      setRepairNote(String(e));
    } finally {
      setRepairing(false);
    }
  };

  const activate = async (path: string, headId: string) => {
    try {
      await app.SetActiveSessionVersion({ scope: "global", topicId: "", path, headId });
      await toggleGroup(path);
      await toggleGroup(path);
    } catch (e) {
      setError(String(e));
    }
  };

  return (
    <div data-testid="session-versions-section" style={{ marginTop: 16 }}>
      <div className="settings-field__label">{t("settings.sessionVersions.title")}</div>
      <div className="settings-field__hint">
        <span className="settings-field__hint-line">{t("settings.sessionVersions.hint")}</span>
      </div>
      {error && (
        <div className="banner banner--error" role="alert">
          {/* 任务 1039r：原因必须跟着横幅走——只有死话时用户与日志都无从
              判断是哪条链路断了（1039 首轮教训在 v3 面板的同款补齐）。 */}
          <span>{error}</span>
        </div>
      )}
      {groups.length === 0 && <div className="settings-field__hint-line">{t("settings.sessionVersions.empty")}</div>}
      <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>
        {groups.map((g) => (
          <li key={g.mainPath} style={{ padding: "6px 0", borderTop: "1px solid var(--border, #333)" }}>
            <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
              <button type="button" onClick={() => void toggleGroup(g.mainPath)} >
                {expanded?.path === g.mainPath ? "▾" : "▸"} {g.mainLabel}
              </button>
              <span>{t("settings.sessionVersions.headCount").replace("{count}", String(g.headCount))}</span>
            </div>
            {expanded?.path === g.mainPath && !expanded.loading && (
              <div style={{ paddingLeft: 16 }}>
                <table style={{ borderCollapse: "collapse", width: "100%", marginTop: 4 }}>
                  <thead>
                    <tr>
                      <th style={{ textAlign: "left" }}>{t("settings.sessionVersions.kind")}</th>
                      <th style={{ textAlign: "right" }}>{t("settings.sessionVersions.messages")}</th>
                      <th style={{ textAlign: "right" }}>{t("settings.sessionVersions.turns")}</th>
                      <th style={{ textAlign: "left" }}>{t("settings.sessionVersions.lastActivity")}</th>
                      <th style={{ textAlign: "left" }}>{t("settings.sessionVersions.preview")}</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {(chains.chains?.chains ?? []).map((c) => {
                      const isSuggested = chains.chains?.longestHead === c.headId && !c.selected;
                      return (
                        <tr key={`${c.path}|${c.headId}`}>
                          <td>
                            {c.headId ? c.headId.slice(0, 8) : "main"}
                            {/* 任务 1039r：降级行必须说自己是降级——否则零计数行
                                和「空会话」在面板上无法区分（1039 首轮用户预期
                                「Degraded 行」而面板根本不渲染标记的缺口）。 */}
                            {c.degraded && (
                              <span className="sv-degraded" title={t("settings.sessionVersions.degradedHint")}>
                                {t("settings.sessionVersions.degraded")}
                              </span>
                            )}
                          </td>
                          <td style={{ textAlign: "right" }}>{c.messageCount}</td>
                          <td style={{ textAlign: "right" }}>{c.turns}</td>
                          <td>{formatWhen(c.lastActivity)}</td>
                          <td style={{ maxWidth: 280, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{c.preview}</td>
                          <td style={{ whiteSpace: "nowrap" }}>
                            {c.selected && <span>{t("settings.sessionVersions.selected")} </span>}
                            {isSuggested && <span>{t("settings.sessionVersions.suggested")} </span>}
                            {!c.selected && (
                              <button type="button" onClick={() => void activate(c.path, c.headId)}>
                                {t("settings.sessionVersions.activate")}
                              </button>
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
                <div style={{ display: "flex", gap: 8, marginTop: 8, flexWrap: "wrap" }}>
                  <button type="button" onClick={() => void inspect(g.mainPath)}>{t("settings.sessionVersions.check")}</button>
                  <button type="button" disabled={repairing} onClick={() => void repair(g.mainPath, false)}>{t("settings.sessionVersions.repair")}</button>
                  <button type="button" disabled={repairing} onClick={() => void repair(g.mainPath, true)}>{t("settings.sessionVersions.repairDiet")}</button>
                </div>
                {health.path === g.mainPath && health.report && (
                  <div style={{ marginTop: 6 }}>
                    {health.report.healthy ? (
                      <div>{t("settings.sessionVersions.healthy")}</div>
                    ) : (
                      <ul style={{ margin: 0, paddingLeft: 18 }}>
                        {(health.report.issues ?? []).map((issue) => (
                          <li key={issue.code}>
                            <code>{issue.code}</code> {issue.detail}
                            {issue.suggestedAction && <> → {issue.suggestedAction}</>}
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                )}
                {repairNote && <div style={{ marginTop: 6 }}>{repairNote}</div>}
                {records.length > 0 && (
                  <div style={{ marginTop: 6 }}>
                    <div>{t("settings.sessionVersions.repairLog")}</div>
                    <ul style={{ margin: 0, paddingLeft: 18 }}>
                      {records.map((r, i) => (
                        <li key={i}>
                          {formatWhen(r.at)} {r.before} → {r.after}
                          {r.backupDir ? ` · ${r.backupDir}` : ""}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
