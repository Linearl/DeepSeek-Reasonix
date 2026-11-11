import { formatTokens } from "../lib/format";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Brain, Check, ChevronDown, Cpu, Search, Settings } from "lucide-react";
import { asArray } from "../lib/array";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { modelRefDisplayLabel, registerModelRefLabels } from "../lib/modelRefLabel";
import { providerDefaultLabel } from "../lib/providerLabel";
import type { ModelInfo } from "../lib/types";
import { AnchoredPopover } from "./AnchoredPopover";
import { Tooltip } from "./Tooltip";

// ModelSwitcher opens an upward popover listing configured providers. Selecting
// one switches the active model while the current conversation continues.
export function ModelSwitcher({
  label,
  tabId,
  ready = true,
  sessionKey,
  onPick,
  onManage,
  detailLabel,
  details,
  composerMenu = false,
}: {
  label: string;
  detailLabel?: string;
  details?: ReactNode;
  composerMenu?: boolean;
  tabId?: string;
  ready?: boolean;
  sessionKey?: string;
  onPick: (name: string) => boolean | Promise<boolean>;
  onManage?: () => void;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [query, setQuery] = useState("");
  const [triggerWidth, setTriggerWidth] = useState<number | undefined>(undefined);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const loadSeqRef = useRef(0);
  const currentTabKeyRef = useRef(tabId ?? "");
  const pendingPickCountByTabRef = useRef(new Map<string, number>());
  const pickSeqByTabRef = useRef(new Map<string, number>());
  currentTabKeyRef.current = tabId ?? "";

  // Measure trigger width off the render path to avoid forced layout
  useEffect(() => {
    const el = triggerRef.current;
    if (!el) return;
    const measure = () => setTriggerWidth(el.getBoundingClientRect().width);
    measure();
    const observer = new ResizeObserver(() => measure());
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const loadModelsForTab = useCallback((targetTabId?: string) => {
    const targetKey = targetTabId ?? "";
    const seq = ++loadSeqRef.current;
    return (targetTabId ? app.ModelsForTab(targetTabId) : app.Models())
      .then((next) => {
        const list = asArray(next).map(normalizeModelInfo);
        // tl1547 (A1): every catalog snapshot feeds the display-name registry,
        // so ref-holding surfaces elsewhere (sidebar hover, trigger label)
        // resolve the same connection names the settings panel shows.
        registerModelRefLabels(list);
        if (seq === loadSeqRef.current && currentTabKeyRef.current === targetKey) {
          setModels(list);
        }
      })
      .catch(() => {});
  }, []);

  const loadModels = useCallback(
    () => loadModelsForTab(tabId),
    [loadModelsForTab, tabId],
  );

  useEffect(() => {
    void loadModels();
  }, [loadModels, ready, sessionKey, label]);

  useEffect(() => {
    const refresh = () => void loadModels();
    window.addEventListener("reasonix:model-catalog-changed", refresh);
    return () => window.removeEventListener("reasonix:model-catalog-changed", refresh);
  }, [loadModels]);

  useEffect(() => {
    if (open) {
      setQuery("");
      void loadModels();
      window.requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [loadModels, open]);

  const keyword = query.trim().toLowerCase();
  const filtered = useMemo(
    () => keyword
      ? models.filter((m) => m.model.toLowerCase().includes(keyword) || m.provider.toLowerCase().includes(keyword) || (m.displayName ?? "").toLowerCase().includes(keyword))
      : models,
    [models, keyword],
  );

  // Preserve catalog/configuration order, including when the current model changes.
  const groups = useMemo(() => {
    const map = new Map<string, ModelInfo[]>();
    for (const m of filtered) {
      const list = map.get(m.provider);
      if (list) list.push(m);
      else map.set(m.provider, [m]);
    }
    return [...map.entries()]
      .map(([provider, items]) => ({
        provider,
        // tl1547 (A1): the group header is a CONNECTION name — it reads the
        // backend-stamped providerLabel (settings displayName), never the
        // first model's displayName that used to leak in here.
        label: items[0]?.providerLabel?.trim() || providerDefaultLabel(provider),
        items,
      }));
  }, [filtered, t]);

  const currentProvider = useMemo(() => {
    const cur = models.find((m) => m.current) ?? models.find((m) => m.model === label || m.ref === label);
    return cur ? (cur.providerLabel?.trim() || providerDefaultLabel(cur.provider)) : null;
  }, [label, models, t]);
  // tl1547 (A1/A3): the trigger used to render the raw routing ref; it now
  // shows the display form (connection label / model display name) while the
  // matching logic below keeps comparing the raw value.
  const triggerModelLabel = modelRefDisplayLabel(label);
  const triggerLabel = [triggerModelLabel, currentProvider, detailLabel].filter(Boolean).join(" · ");

  const pick = (model: ModelInfo) => {
    setOpen(false);
    const pendingKey = tabId ?? "";
    const pendingPickCount = pendingPickCountByTabRef.current.get(pendingKey) ?? 0;
    // A catalog refresh can still report the outgoing model as current while
    // an earlier switch is rebuilding. In that window, selecting it again is
    // an intentional last-click-wins rollback rather than a no-op.
    if (model.current && pendingPickCount === 0) return;
    const previousModels = models;
    const pickSeq = (pickSeqByTabRef.current.get(pendingKey) ?? 0) + 1;
    pickSeqByTabRef.current.set(pendingKey, pickSeq);
    // Catalog requests started before this click describe the outgoing model
    // and must not overwrite the optimistic last-click choice.
    loadSeqRef.current += 1;
    setModels((prev) => prev.map((m) => ({ ...m, current: m.ref === model.ref })));
    pendingPickCountByTabRef.current.set(pendingKey, pendingPickCount + 1);
    const settlePick = (switched: boolean) => {
      const nextCount = Math.max(
        0,
        (pendingPickCountByTabRef.current.get(pendingKey) ?? 0) - 1,
      );
      if (nextCount === 0) pendingPickCountByTabRef.current.delete(pendingKey);
      else pendingPickCountByTabRef.current.set(pendingKey, nextCount);
      // A superseded completion no longer owns the visible selection. Only the
      // latest failed click may roll back and reconcile with the backend.
      if (
        switched ||
        pickSeqByTabRef.current.get(pendingKey) !== pickSeq ||
        currentTabKeyRef.current !== pendingKey
      ) {
        return;
      }
      setModels(previousModels);
      void loadModelsForTab(tabId);
    };
    try {
      void Promise.resolve(onPick(model.ref)).then(
        (switched) => settlePick(switched),
        () => settlePick(false),
      );
    } catch (err) {
      settlePick(false);
      throw err;
    }
  };

  return (
    <div className="modelsw">
      <Tooltip label={triggerLabel} fill>
        <button
          ref={triggerRef}
          type="button"
          className="modelsw__trigger"
          aria-label={triggerLabel}
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          <Cpu size={14} className="modelsw__kind" />
          <span className="modelsw__label">{triggerModelLabel}{detailLabel && <span className="modelsw__detail"> · {detailLabel}</span>}</span>
          <ChevronDown size={12} />
        </button>
      </Tooltip>
      <AnchoredPopover
        open={open}
        anchorRef={triggerRef}
        onClose={() => setOpen(false)}
        className={`modelsw__menu modelsw__menu--portal${composerMenu ? " composer-menu-surface" : ""}`}
        style={composerMenu ? undefined : { minWidth: Math.max(triggerWidth || 200, 200), maxWidth: "min(90vw, 480px)" }}
      >
        <div role="listbox">
          <div className="modelsw__search" role="presentation">
            <Search size={13} />
            <input
              ref={inputRef}
              type="text"
              className="modelsw__search-input"
              placeholder={t("modelSwitcher.searchPlaceholder")}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Escape") setOpen(false);
                if (e.key === "Enter" && filtered.length === 1) pick(filtered[0]);
              }}
            />
          </div>
          {models.length === 0 && <div className="modelsw__empty">{t("status.noModels")}</div>}
          {models.length > 0 && filtered.length === 0 && query && <div className="modelsw__empty">{t("modelSwitcher.noMatches")}</div>}
          {groups.map((g) => (
            <div key={g.provider} role="group" aria-label={g.label} className="modelsw__group">
              <div className="modelsw__group-label" role="presentation"><Brain size={11} />{g.label}</div>
              {g.items.map((m) => (
                <button
                  key={m.ref}
                  type="button"
                  role="option"
                  aria-selected={m.current}
                  className={`modelsw__item ${m.current ? "modelsw__item--current" : ""}`}
                  onClick={() => pick(m)}
                >
                  <span className="modelsw__copy">
                    {/* tl1547 (A3): the per-model display name the backend
                        already provides wins over the bare model id. */}
                    <span className="modelsw__model">{m.displayName?.trim() || m.model}</span>
                  </span>
                  {m.contextWindow ? <span className="badge badge--neutral">{formatTokens(m.contextWindow)}</span> : null}
                  {m.vision && <span className="badge badge--neutral">{t("providerUI.image")}</span>}
                  {m.current && <Check size={13} className="modelsw__check" />}
                </button>
              ))}
            </div>
          ))}
        </div>
        {details}
        {onManage && <button className="modelsw__item modelsw__manage" type="button" onClick={() => { setOpen(false); onManage(); }}><Settings size={14} />{t("providerUI.manageModels")}</button>}
      </AnchoredPopover>
    </div>
  );
}

export function normalizeModelInfo(model: ModelInfo): ModelInfo {
  return {
    ...model,
    provider: String(model.provider ?? ""),
    model: String(model.model ?? ""),
  };
}

