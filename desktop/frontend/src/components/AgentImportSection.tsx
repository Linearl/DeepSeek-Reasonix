import { useCallback, useMemo, useState } from "react";
import { ChevronDown, ChevronRight, RefreshCw } from "lucide-react";
import { app } from "../lib/bridge";
import { useT, type DictKey } from "../lib/i18n";
import type { AgentImportItemView, AgentImportReceiptView, AgentImportScanView } from "../lib/types";

// AgentImportSection (tl1422) is the settings-page surface for importing
// commands/skills/MCP config from locally installed external AI coding tools
// (Claude Code, Codex, OpenCode). It renders the scan result, per-item
// checkboxes with content preview, conflict handling (skip/overwrite/rename),
// and the import receipt. Scanning and importing never touch the external
// tools' files — the backend is read-only on those trees.
type ConflictPolicy = "skip" | "overwrite" | "rename";

function toolLabelKey(tool: string): DictKey {
  switch (tool) {
    case "claude":
      return "caps.importToolClaude";
    case "codex":
      return "caps.importToolCodex";
    case "opencode":
      return "caps.importToolOpenCode";
    default:
      return "caps.importToolUnknown";
  }
}

function kindLabelKey(kind: string): DictKey {
  switch (kind) {
    case "command":
      return "caps.importKindCommand";
    case "skill":
      return "caps.importKindSkill";
    case "mcp":
      return "caps.importKindMCP";
    default:
      return kind as DictKey;
  }
}

function ImportItemRow({
  item,
  checked,
  policy,
  expanded,
  busy,
  onToggle,
  onPolicy,
  onExpand,
}: {
  item: AgentImportItemView;
  checked: boolean;
  policy: ConflictPolicy;
  expanded: boolean;
  busy: boolean;
  onToggle: (next: boolean) => void;
  onPolicy: (next: ConflictPolicy) => void;
  onExpand: () => void;
}) {
  const t = useT();
  return (
    <div className={`agent-import__item${item.conflict ? " agent-import__item--conflict" : ""}`}>
      <label className="agent-import__pick">
        <input type="checkbox" checked={checked} disabled={busy} onChange={(e) => onToggle(e.target.checked)} />
        <span className="agent-import__name" title={item.sourcePath}>{item.name}</span>
        <span className="cap-skill-badge">{t(kindLabelKey(item.kind))}</span>
        {item.conflict && <span className="cap-skill-badge cap-skill-badge--off">{t("caps.importConflict")}</span>}
        {item.nativelyActive && <span className="cap-skill-badge">{t("caps.importNativelyActive")}</span>}
        {(item.notes ?? []).map((note) => (
          <span key={note} className="cap-skill-badge cap-skill-badge--off">{t("caps.importDisabledNote")}</span>
        ))}
      </label>
      {item.description && <div className="agent-import__desc">{item.description}</div>}
      <div className="agent-import__meta">
        <button className="agent-import__preview-toggle" type="button" onClick={onExpand} aria-expanded={expanded}>
          {expanded ? <ChevronDown aria-hidden size={12} /> : <ChevronRight aria-hidden size={12} />}
          <span>{t("caps.importPreview")}</span>
        </button>
        {item.conflict && checked && (
          <label className="agent-import__policy">
            <span>{t("caps.importPolicy")}</span>
            <select value={policy} disabled={busy} onChange={(e) => onPolicy(e.target.value as ConflictPolicy)}>
              <option value="skip">{t("caps.importPolicySkip")}</option>
              <option value="overwrite">{t("caps.importPolicyOverwrite")}</option>
              <option value="rename">{t("caps.importPolicyRename")}</option>
            </select>
          </label>
        )}
      </div>
      {expanded && <pre className="agent-import__preview">{item.preview || "—"}</pre>}
    </div>
  );
}

export function AgentImportSection() {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [importing, setImporting] = useState(false);
  const [scan, setScan] = useState<AgentImportScanView | null>(null);
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [policies, setPolicies] = useState<Record<string, ConflictPolicy>>({});
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const [receipt, setReceipt] = useState<AgentImportReceiptView | null>(null);
  const [showFailures, setShowFailures] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const runScan = useCallback(async () => {
    setScanning(true);
    setError(null);
    try {
      const next = await app.AgentImportScan();
      setScan(next);
      // Default selection: recommended non-conflicting items; natively-active
      // ones stay unchecked because Reasonix already discovers them.
      const defaults = new Set<string>();
      for (const item of next.items ?? []) {
        if (item.recommended && !item.conflict && !item.nativelyActive) defaults.add(item.id);
      }
      setSelected(defaults);
      setPolicies({});
      setReceipt(null);
      setShowFailures(false);
    } catch (e) {
      setError(String((e as Error)?.message ?? e));
    } finally {
      setScanning(false);
    }
  }, []);

  const importSelected = useCallback(async () => {
    if (!scan) return;
    setImporting(true);
    setError(null);
    try {
      const requests = (scan.items ?? [])
        .filter((item) => selected.has(item.id))
        .map((item) => ({ id: item.id, policy: policies[item.id] ?? "skip" }));
      if (requests.length === 0) {
        setError(t("caps.importNoneSelected"));
        return;
      }
      const result = await app.AgentImportImport(requests);
      setReceipt(result);
      setShowFailures(false);
      setScan(await app.AgentImportScan());
    } catch (e) {
      setError(String((e as Error)?.message ?? e));
    } finally {
      setImporting(false);
    }
  }, [policies, scan, selected, t]);

  const grouped = useMemo(() => {
    const byTool = new Map<string, AgentImportItemView[]>();
    for (const item of scan?.items ?? []) {
      const list = byTool.get(item.tool) ?? [];
      list.push(item);
      byTool.set(item.tool, list);
    }
    return [...byTool.entries()];
  }, [scan]);

  const toggle = useCallback((id: string, next: boolean) => {
    setSelected((prev) => {
      const nextSet = new Set(prev);
      if (next) nextSet.add(id);
      else nextSet.delete(id);
      return nextSet;
    });
  }, []);

  const expand = useCallback((id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const busy = scanning || importing;
  const selectedCount = selected.size;
  const failedItems = (receipt?.items ?? []).filter((item) => item.status === "failed");

  return (
    <section className="agent-import">
      <button className="agent-import__head" type="button" onClick={() => setOpen((prev) => !prev)} aria-expanded={open}>
        {open ? <ChevronDown aria-hidden size={14} /> : <ChevronRight aria-hidden size={14} />}
        <span className="agent-import__title">{t("caps.import")}</span>
        <span className="agent-import__hint">{t("caps.importHint")}</span>
      </button>
      {open && (
        <div className="agent-import__body">
          <div className="agent-import__toolbar">
            <button className="btn btn--secondary btn--small" type="button" onClick={() => void runScan()} disabled={busy}>
              <RefreshCw aria-hidden size={12} />
              <span>{scanning ? t("caps.importScanning") : t("caps.importScan")}</span>
            </button>
            {scan && (scan.items ?? []).length > 0 && (
              <button className="btn btn--primary btn--small" type="button" onClick={() => void importSelected()} disabled={busy || selectedCount === 0}>
                <span>{importing ? t("caps.importImporting") : t("caps.importDo", { count: selectedCount })}</span>
              </button>
            )}
          </div>
          {error && <div className="banner banner--error" role="alert"><span>{error}</span></div>}
          {scan && (
            <div className="agent-import__sources">
              {(scan.sources ?? []).map((src) => (
                <div key={src.tool} className={`agent-import__source${src.detected ? "" : " agent-import__source--missing"}`}>
                  <span className="agent-import__source-name">{t(toolLabelKey(src.tool))}</span>
                  {src.detected ? (
                    <span className="cap-skill-badge">{t("caps.importItems", { count: src.items })}</span>
                  ) : (
                    <span className="cap-skill-badge cap-skill-badge--off">{t("caps.importNotDetected")}</span>
                  )}
                </div>
              ))}
            </div>
          )}
          {scan && (scan.items ?? []).length === 0 && (
            <div className="mem-empty">{t("caps.importNone")}</div>
          )}
          {grouped.map(([tool, items]) => (
            <div key={tool} className="agent-import__group">
              <div className="agent-import__group-title">{t(toolLabelKey(tool))}</div>
              {items.map((item) => (
                <ImportItemRow
                  key={item.id}
                  item={item}
                  checked={selected.has(item.id)}
                  policy={policies[item.id] ?? "skip"}
                  expanded={expanded.has(item.id)}
                  busy={busy}
                  onToggle={(next) => toggle(item.id, next)}
                  onPolicy={(next) => setPolicies((prev) => ({ ...prev, [item.id]: next }))}
                  onExpand={() => expand(item.id)}
                />
              ))}
            </div>
          ))}
          {receipt && (
            <div className={`banner ${receipt.failed > 0 ? "banner--warning" : "banner--success"} agent-import__receipt`} role="status">
              <span>{t("caps.importReceipt", { imported: receipt.imported, skipped: receipt.skipped, failed: receipt.failed })}</span>
              {failedItems.length > 0 && (
                <>
                  <button className="btn btn--secondary btn--small" type="button" onClick={() => setShowFailures((prev) => !prev)} aria-expanded={showFailures}>
                    {showFailures ? <ChevronDown aria-hidden size={12} /> : <ChevronRight aria-hidden size={12} />}
                    <span>{t("caps.importFailures", { count: failedItems.length })}</span>
                  </button>
                  {showFailures && (
                    <ul className="agent-import__failures">
                      {failedItems.map((item) => (
                        <li key={item.id}>
                          <span className="agent-import__failure-name">{item.name}</span>
                          <span className="agent-import__failure-detail">{item.detail}</span>
                        </li>
                      ))}
                    </ul>
                  )}
                </>
              )}
            </div>
          )}
        </div>
      )}
    </section>
  );
}
