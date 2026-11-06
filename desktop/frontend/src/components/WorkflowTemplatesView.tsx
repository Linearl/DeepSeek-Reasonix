// tl1454 —— 工作流模板视图（工作流面板的二级视图，懒加载 chunk）。
//
// 只在实验开关 workflowTemplates 开启时由 WorkflowPanel 懒加载挂载；关闭时
// 面板 DOM 与本 chunk 都不存在（铁律 2）。首版是「文本编辑 + 定位校验 +
// 契约摘要 + 保存（含历史）+ 运行前确认」，不做图形编辑器（non-goal）。
// 校验/保存/运行全部走桌面桥 → workflow 服务的同一 schema 与同一引擎。

import { useCallback, useEffect, useRef, useState } from "react";
import { FilePlus2, History, Play, ShieldAlert, ShieldCheck } from "lucide-react";
import { useT } from "../lib/i18n";
import { app } from "../lib/bridge";
import { reportFrontendLog } from "../lib/frontendLog";
import { diagKindKey, lineColumnOffset, NEW_TEMPLATE_SKELETON } from "../lib/workflowTemplates";
import type {
  WorkflowTemplateHistoryEntry,
  WorkflowTemplateList,
  WorkflowTemplateRunPreview,
  WorkflowTemplateValidation,
} from "../lib/workflowTypes";

const VALIDATE_DEBOUNCE_MS = 400;
const REPAIR_VERSION = "repair";

type Selection =
  | { kind: "template"; name: string }
  | { kind: "invalid"; file: string }
  | { kind: "new" };

function errorText(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

export function WorkflowTemplatesView({ onRunStarted }: { onRunStarted: (runId: string) => void }) {
  const t = useT();
  const [list, setList] = useState<WorkflowTemplateList | null>(null);
  const [selection, setSelection] = useState<Selection | null>(null);
  const [text, setText] = useState("");
  const [savedText, setSavedText] = useState("");
  const [expected, setExpected] = useState("");
  const [validation, setValidation] = useState<WorkflowTemplateValidation | null>(null);
  const [notice, setNotice] = useState<{ tone: "ok" | "error"; text: string } | null>(null);
  const [history, setHistory] = useState<WorkflowTemplateHistoryEntry[] | null>(null);
  const [preview, setPreview] = useState<WorkflowTemplateRunPreview | null>(null);
  const [busy, setBusy] = useState(false);
  const editorRef = useRef<HTMLTextAreaElement | null>(null);
  const validateSeq = useRef(0);

  const fail = useCallback((what: string, err: unknown) => {
    const detail = errorText(err);
    reportFrontendLog("workflowTemplates", `${what} failed`, detail);
    setNotice({ tone: "error", text: t("workflowPanel.templates.error", { detail }) });
  }, [t]);

  const refreshList = useCallback(async () => {
    try {
      setList(await app.WorkflowTemplates());
    } catch (err) {
      fail("list", err);
    }
  }, [fail]);

  useEffect(() => {
    void refreshList();
  }, [refreshList]);

  const runValidate = useCallback(async (doc: string) => {
    const seq = ++validateSeq.current;
    try {
      const result = await app.WorkflowTemplateValidate(doc);
      if (seq === validateSeq.current) setValidation(result);
    } catch (err) {
      if (seq === validateSeq.current) fail("validate", err);
    }
  }, [fail]);

  // 输入停顿即校验（只读、不写盘）。
  useEffect(() => {
    if (selection === null) return;
    const timer = window.setTimeout(() => void runValidate(text), VALIDATE_DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [text, selection, runValidate]);

  const loadInto = useCallback((doc: string, version: string, next: Selection) => {
    setSelection(next);
    setText(doc);
    setSavedText(doc);
    setExpected(version);
    setHistory(null);
    setPreview(null);
    setNotice(null);
    setValidation(null);
  }, []);

  const openTemplate = useCallback(async (name: string) => {
    try {
      const src = await app.WorkflowTemplateSource(name);
      loadInto(src.document, src.version, { kind: "template", name });
    } catch (err) {
      fail("open", err);
    }
  }, [fail, loadInto]);

  const openInvalid = useCallback(async (file: string) => {
    try {
      const raw = await app.WorkflowTemplateInvalidSource(file);
      loadInto(raw, REPAIR_VERSION, { kind: "invalid", file });
    } catch (err) {
      fail("open invalid", err);
    }
  }, [fail, loadInto]);

  const createNew = useCallback(() => {
    loadInto(NEW_TEMPLATE_SKELETON, "", { kind: "new" });
    setSavedText("");
  }, [loadInto]);

  const save = useCallback(async () => {
    setBusy(true);
    try {
      const res = await app.WorkflowTemplateSave(text, expected);
      setSavedText(text);
      setExpected(res.version);
      setSelection({ kind: "template", name: res.name });
      setHistory(null);
      setNotice({ tone: "ok", text: t("workflowPanel.templates.saved", { version: res.version }) });
      reportFrontendLog("workflowTemplates", "saved", `${res.name} ${res.version}`);
      await refreshList();
    } catch (err) {
      fail("save", err);
    } finally {
      setBusy(false);
    }
  }, [text, expected, t, fail, refreshList]);

  const toggleHistory = useCallback(async () => {
    if (history !== null || selection?.kind !== "template") {
      setHistory(null);
      return;
    }
    try {
      setHistory(await app.WorkflowTemplateHistory(selection.name));
    } catch (err) {
      fail("history", err);
    }
  }, [history, selection, fail]);

  const loadVersion = useCallback(async (version: string) => {
    if (selection?.kind !== "template") return;
    try {
      // 载入旧版本只改编辑器；保存时 expected 仍是当前 head = 「恢复」也是一次
      // 普通覆盖（当前 head 先入历史，不丢）。
      setText(await app.WorkflowTemplateVersionSource(selection.name, version));
      setHistory(null);
    } catch (err) {
      fail("load version", err);
    }
  }, [selection, fail]);

  const askRun = useCallback(async () => {
    if (selection?.kind !== "template") return;
    try {
      setPreview(await app.WorkflowTemplateRunPreview(selection.name));
    } catch (err) {
      fail("preview", err);
    }
  }, [selection, fail]);

  const confirmRun = useCallback(async () => {
    if (!preview) return;
    setBusy(true);
    try {
      const res = await app.WorkflowTemplateRun(preview.name);
      reportFrontendLog("workflowTemplates", "run started", `${preview.name} ${res.runId}`);
      setPreview(null);
      onRunStarted(res.runId);
    } catch (err) {
      fail("run", err);
    } finally {
      setBusy(false);
    }
  }, [preview, fail, onRunStarted]);

  const jumpTo = useCallback((line?: number, column?: number) => {
    const editor = editorRef.current;
    if (!editor || !line) return;
    const at = lineColumnOffset(editor.value, line, column);
    editor.focus();
    editor.setSelectionRange(at, at);
  }, []);

  const dirty = text !== savedText;
  const diag = validation && !validation.ok ? validation.diagnostic : undefined;
  const canSave = !busy && selection !== null && validation?.ok === true && dirty;
  const canRun = !busy && selection?.kind === "template" && !dirty && validation?.ok === true;

  return (
    <div className="wf-tpl" data-testid="wf-templates">
      <aside className="wf-tpl__list">
        <button type="button" className="wf-panel__action wf-tpl__new" onClick={createNew} data-testid="wf-tpl-new">
          <FilePlus2 size={13} aria-hidden="true" />
          {t("workflowPanel.templates.new")}
        </button>
        {list && list.templates.length === 0 && (list.invalid ?? []).length === 0 && (
          <p className="wf-tpl__empty">{t("workflowPanel.templates.empty")}</p>
        )}
        {list?.templates.map((tpl) => (
          <button
            key={tpl.name}
            type="button"
            className={selection?.kind === "template" && selection.name === tpl.name ? "wf-tpl__item wf-tpl__item--active" : "wf-tpl__item"}
            onClick={() => void openTemplate(tpl.name)}
            data-testid="wf-tpl-item"
          >
            <span className="wf-tpl__item-name">{tpl.name}</span>
            <span className="wf-tpl__item-meta">{tpl.version}</span>
          </button>
        ))}
        {(list?.invalid ?? []).map((inv) => (
          <button
            key={inv.file}
            type="button"
            className={selection?.kind === "invalid" && selection.file === inv.file ? "wf-tpl__item wf-tpl__item--invalid wf-tpl__item--active" : "wf-tpl__item wf-tpl__item--invalid"}
            title={inv.message}
            onClick={() => void openInvalid(inv.file)}
            data-testid="wf-tpl-invalid"
          >
            <span className="wf-tpl__item-name">{inv.file}</span>
            <span className="wf-tpl__badge">{t("workflowPanel.templates.invalidBadge")}</span>
          </button>
        ))}
      </aside>
      <section className="wf-tpl__main">
        {selection === null ? (
          <p className="wf-tpl__empty">{t("workflowPanel.templates.title")}</p>
        ) : (
          <>
            <div className="wf-tpl__bar">
              <span className="wf-tpl__bar-name">
                {selection.kind === "template" ? selection.name : selection.kind === "invalid" ? selection.file : t("workflowPanel.templates.new")}
              </span>
              {selection.kind === "template" && expected && (
                <span className="wf-tpl__item-meta" data-testid="wf-tpl-version">{t("workflowPanel.templates.version", { version: expected })}</span>
              )}
              {dirty && <span className="wf-tpl__dirty">{t("workflowPanel.templates.dirty")}</span>}
              <span className="wf-tpl__spacer" />
              <button type="button" className="wf-panel__action" onClick={() => void runValidate(text)} data-testid="wf-tpl-validate">
                {t("workflowPanel.templates.validate")}
              </button>
              {selection.kind === "template" && (
                <button type="button" className="wf-panel__action" onClick={() => void toggleHistory()} data-testid="wf-tpl-history">
                  <History size={13} aria-hidden="true" />
                  {t("workflowPanel.templates.history")}
                </button>
              )}
              <button type="button" className="wf-panel__action" disabled={!canSave} onClick={() => void save()} data-testid="wf-tpl-save">
                {t("workflowPanel.templates.save")}
              </button>
              <button type="button" className="wf-panel__action" disabled={!canRun} onClick={() => void askRun()} data-testid="wf-tpl-run">
                <Play size={13} aria-hidden="true" />
                {t("workflowPanel.templates.run")}
              </button>
            </div>
            {selection.kind === "invalid" && <p className="wf-tpl__note">{t("workflowPanel.templates.invalidHint")}</p>}
            {history !== null && (
              <div className="wf-tpl__history" data-testid="wf-tpl-history-list">
                {history.length === 0 ? (
                  <p className="wf-tpl__note">{t("workflowPanel.templates.historyEmpty")}</p>
                ) : (
                  history.map((h) => (
                    <div key={h.fingerprint + h.savedAt} className="wf-tpl__history-row">
                      <span className="wf-tpl__item-meta">{h.version}</span>
                      <span className="wf-tpl__item-meta">{new Date(h.savedAt).toLocaleString()}</span>
                      <button type="button" className="wf-panel__action" onClick={() => void loadVersion(h.version)}>
                        {t("workflowPanel.templates.historyLoad")}
                      </button>
                    </div>
                  ))
                )}
              </div>
            )}
            <textarea
              ref={editorRef}
              className="wf-tpl__editor"
              spellCheck={false}
              value={text}
              onChange={(e) => setText(e.target.value)}
              aria-label={t("workflowPanel.templates.title")}
              data-testid="wf-tpl-editor"
            />
            <div className="wf-tpl__status" data-testid="wf-tpl-status">
              {diag ? (
                <button type="button" className="wf-tpl__diag" onClick={() => jumpTo(diag.line, diag.column)} data-testid="wf-tpl-diag">
                  <span className="wf-tpl__diag-human">{t(diagKindKey(diag.kind))}</span>
                  <span className="wf-tpl__diag-where">
                    {diag.line ? t("workflowPanel.templates.diagAt", { line: String(diag.line), column: String(diag.column ?? 1) }) : ""}
                    {diag.nodeId ? ` · ${t("workflowPanel.templates.diagNode", { node: diag.nodeId })}` : ""}
                    {diag.path ? ` · ${diag.path}` : ""}
                  </span>
                  <span className="wf-tpl__diag-tech">{t("workflowPanel.templates.technical")}: {diag.message}</span>
                </button>
              ) : validation?.ok && validation.summary ? (
                <span className="wf-tpl__ok" data-testid="wf-tpl-valid">
                  {t("workflowPanel.templates.valid", { version: validation.summary.version })}
                </span>
              ) : null}
              {notice && (
                <span className={notice.tone === "ok" ? "wf-tpl__ok" : "wf-tpl__err"} data-testid="wf-tpl-notice">{notice.text}</span>
              )}
              <span className="wf-tpl__note">{t("workflowPanel.templates.canonicalNote")}</span>
            </div>
            {validation?.ok && (validation.contracts ?? []).length > 0 && (
              <details className="wf-tpl__contracts" data-testid="wf-tpl-contracts">
                <summary>{t("workflowPanel.templates.contract.title")}</summary>
                <table>
                  <thead>
                    <tr>
                      <th>id</th>
                      <th>{t("workflowPanel.templates.contract.inputs")}</th>
                      <th>{t("workflowPanel.templates.contract.outputs")}</th>
                      <th>{t("workflowPanel.templates.contract.failure")}</th>
                      <th>{t("workflowPanel.templates.contract.limits")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(validation.contracts ?? []).map((c) => (
                      <tr key={c.id}>
                        <td>{c.id} <span className="wf-tpl__item-meta">{c.type}</span></td>
                        <td>{c.inputs || "—"}</td>
                        <td>{c.outputs || "—"}</td>
                        <td>{c.failure}</td>
                        <td>{c.limits || "—"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </details>
            )}
          </>
        )}
      </section>
      {preview && (
        <div className="wf-tpl__confirm" role="dialog" aria-modal="true" aria-label={t("workflowPanel.templates.confirm.title")} data-testid="wf-tpl-confirm">
          <h3>{t("workflowPanel.templates.confirm.title")} · {preview.name} {preview.version}</h3>
          <p className="wf-tpl__note">
            {preview.workspace
              ? t("workflowPanel.templates.confirm.workspace", { path: preview.workspace })
              : t("workflowPanel.templates.confirm.noWorkspace")}
          </p>
          <div className="wf-tpl__confirm-section">{t("workflowPanel.templates.confirm.gates")}</div>
          {preview.gates.length === 0 ? (
            <p className="wf-tpl__note">{t("workflowPanel.templates.confirm.noGates")}</p>
          ) : (
            preview.gates.map((g) => (
              <div key={g.nodeId} className={g.denied ? "wf-tpl__gate wf-tpl__gate--denied" : "wf-tpl__gate"} data-testid="wf-tpl-gate">
                {g.denied ? <ShieldAlert size={13} aria-hidden="true" /> : <ShieldCheck size={13} aria-hidden="true" />}
                <span className="wf-tpl__item-meta">{g.nodeId}</span>
                <code>{g.command}</code>
                {g.denied && <span className="wf-tpl__err">{t("workflowPanel.templates.confirm.denied")}</span>}
              </div>
            ))
          )}
          <div className="wf-tpl__confirm-section">{t("workflowPanel.templates.confirm.subagents")}</div>
          {preview.subagents.map((sa) => (
            <div key={sa.nodeId} className="wf-tpl__gate">
              <span className="wf-tpl__item-meta">{sa.nodeId}</span>
              <span>
                {sa.readOnly
                  ? t("workflowPanel.templates.confirm.readOnly")
                  : sa.allTools
                    ? t("workflowPanel.templates.confirm.allTools")
                    : (sa.tools ?? []).join(", ")}
                {sa.timeout ? ` · timeout ${sa.timeout}` : ""}
                {sa.maxSteps ? ` · max_steps ${sa.maxSteps}` : ""}
              </span>
            </div>
          ))}
          <div className="wf-tpl__confirm-section">{t("workflowPanel.templates.confirm.budget")}</div>
          <p className="wf-tpl__note">
            {preview.budget
              ? [preview.budget.tokens ? `tokens ${preview.budget.tokens}` : "", preview.budget.rounds ? `rounds ${preview.budget.rounds}` : "", preview.budget.wall ? `wall ${preview.budget.wall}` : ""].filter(Boolean).join(" · ")
              : t("workflowPanel.templates.confirm.noBudget")}
          </p>
          <p className="wf-tpl__warn">{t("workflowPanel.templates.confirm.warning")}</p>
          {preview.blocked && <p className="wf-tpl__err">{t("workflowPanel.templates.confirm.blocked")}</p>}
          <div className="wf-tpl__confirm-actions">
            <button type="button" className="wf-panel__action" onClick={() => setPreview(null)} data-testid="wf-tpl-confirm-cancel">
              {t("workflowPanel.templates.confirm.cancel")}
            </button>
            <button type="button" className="wf-panel__action wf-tpl__primary" disabled={preview.blocked || busy} onClick={() => void confirmRun()} data-testid="wf-tpl-confirm-run">
              <Play size={13} aria-hidden="true" />
              {t("workflowPanel.templates.confirm.run")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
