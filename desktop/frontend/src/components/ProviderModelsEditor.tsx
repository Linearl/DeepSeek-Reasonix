import { useProviderT as useT } from "../lib/providerSettingsLocale";
import { Suspense, lazy, useEffect, useMemo, useRef, useState } from "react";
import type { ModelDetailsDraft } from "./ProviderModelDialog";
import { Plus, Plug, RefreshCw, Search, SlidersHorizontal, Trash2 } from "lucide-react";

import type { ProviderModelCapabilityView, ProviderModelOverrideView, ProviderView } from "../lib/types";
import { applyModelDraft } from "../lib/providerModelDraft";
import { providerVisionModelsForView } from "../lib/providerVisionCapability";
import { ModelImageInputControl } from "./ModelImageInputControl";
import { imageInputModeForModel, imageInputModes, mergeImageInputModes, modelCapabilityForModel, matchingModelKey } from "../lib/providerImageInput";
import { ProviderDialog } from "./ProviderDialog";

// tl 1104: 上区（ProviderEditorModelPicker）与本组件是同一「模型设置」入口的两个挂载点，
// 编辑弹窗统一为功能更多的 ProviderModelDialog（含音频输入 / 高速模型）。lazy 与上区
// 同款——弹窗代码不进启动包。
const ProviderModelDialog = lazy(() => import("./ProviderModelDialog"));

// #33: what the probe measures on top of connectivity. Zero timing means the
// endpoint did not stream, so the row keeps showing plain success.
export type ModelProbeResult = {
  ok: boolean; ttftMs?: number; generationMs?: number; outputTokens?: number; tps?: number; preview?: string; error?: string;
};

export function ProviderModelsEditor({ provider, disabled, canFetch, onChange, onFetch, onTest, probeKey }: {
  provider: ProviderView; probeKey?: string; disabled: boolean; canFetch: boolean;
  onChange: (models: string[], overrides: ProviderModelOverrideView[], capabilities: ProviderModelCapabilityView[]) => void;
  onFetch: () => Promise<ProviderModelCapabilityView[]>;
  onTest: (model: string) => Promise<ModelProbeResult>;
}) {
  const t = useT();
  const [editor, setEditor] = useState<{ original?: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [discovery, setDiscovery] = useState<ProviderModelCapabilityView[] | null>(null);
  const [selection, setSelection] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [fetching, setFetching] = useState(false);
  const [tests, setTests] = useState<Record<string, { busy: boolean; error?: string; probe?: ModelProbeResult }>>({});
  // An endpoint/key/parameter edit invalidates every in-flight result for this draft.
  const identity = JSON.stringify([provider, probeKey]);
  const identityRef = useRef(identity); identityRef.current = identity;
  const epoch = useRef(0);
  useEffect(() => { epoch.current += 1; setFetching(false); setTests({}); setDiscovery(null); setError(null); }, [identity]);
  useEffect(() => () => { epoch.current += 1; }, []);
  const filteredDiscovery = discovery?.filter((item) => item.model.toLowerCase().includes(query.trim().toLowerCase())) ?? [];
  const overrides = provider.modelOverrides ?? [];
  const capabilities = provider.modelCapabilities ?? [];
  const vision = providerVisionModelsForView(provider);
  // Task 1053: the connection-level vocabulary a model inherits when it does
  // not declare its own supported_efforts — same precedence the backend
  // applies (model override > connection entry > protocol default).
  const connectionLevels = useMemo(
    () => (provider.supportedEfforts ?? []).filter((level) => level.trim() && level.trim().toLowerCase() !== "auto"),
    [provider.supportedEfforts],
  );
  const effortLevelsFor = (model: string): string[] => {
    const key = matchingModelKey(overrides.map((item) => item.model), model);
    const override = key ? overrides.find((item) => item.model === key) : undefined;
    return override?.supportedEfforts?.length ? override.supportedEfforts : connectionLevels;
  };
  const openEditor = (model?: string) => {
    setError(null);
    setEditor({ original: model });
  };
  const contextWindowOf = (model: string) => {
    const override = overrides.find((item) => item.model === matchingModelKey(overrides.map((item) => item.model), model));
    return override?.contextWindow ? String(override.contextWindow) : "";
  };
  const outputTokensOf = (model: string) => {
    const override = overrides.find((item) => item.model === matchingModelKey(overrides.map((item) => item.model), model));
    return override?.maxOutputTokens ?? 0;
  };
  const visionOf = (model: string): boolean | null => overrides.find((item) => item.model === matchingModelKey(overrides.map((item) => item.model), model))?.vision ?? null;
  const audioOf = (model: string): boolean | null => overrides.find((item) => item.model === matchingModelKey(overrides.map((item) => item.model), model))?.audio ?? null;
  const supportedEffortsOf = (model: string): string[] => overrides.find((item) => item.model === matchingModelKey(overrides.map((item) => item.model), model))?.supportedEfforts ?? [];
  const fetchModels = async () => {
    const generation = epoch.current, fingerprint = identity;
    setFetching(true); setError(null);
    try {
      const items = await onFetch();
      if (generation !== epoch.current || identityRef.current !== fingerprint) return;
      setDiscovery(Array.from(new Map(items.map((item) => [item.model, item])).values()));
      setSelection([]); setQuery("");
    } catch (e) {
      if (generation === epoch.current && identityRef.current === fingerprint) setError(String((e as Error).message ?? e));
    } finally { if (generation === epoch.current && identityRef.current === fingerprint) setFetching(false); }
  };
  const test = async (model: string) => {
    const generation = epoch.current, fingerprint = identity;
    setTests((prev) => ({ ...prev, [model]: { busy: true } }));
    try {
      const probe = await onTest(model);
      if (generation === epoch.current && identityRef.current === fingerprint) setTests((prev) => ({ ...prev, [model]: { busy: false, probe } }));
    } catch (e) {
      if (generation === epoch.current && identityRef.current === fingerprint) setTests((prev) => ({ ...prev, [model]: { busy: false, error: String((e as Error).message ?? e) } }));
    }
  };
  // tl 1104：与上区 applyModelDetails 同语义的应用——模型 ID 改名时，发现的
  // capability 描述属于旧 ID（远端描述不能随 ID 搬运），丢弃它。
  const applyModel = (draft: ModelDetailsDraft) => {
    if (!editor) return;
    const model = draft.model.trim();
    const models = editor.original ? provider.models.map((name) => name === editor.original ? model : name) : [...provider.models, model];
    const nextCapabilities = model !== editor.original ? capabilities.filter((item) => item.model !== editor.original) : capabilities;
    onChange(models, applyModelDraft(overrides, draft, editor.original), nextCapabilities);
    setEditor(null); setError(null);
  };
  return <section className="provider-models-editor">
    <div className="provider-models-editor__head"><strong>{t("settings.modelList")}</strong><div>
      <button type="button" className="btn btn--secondary btn--small" disabled={disabled || fetching || !canFetch} onClick={() => void fetchModels()}><RefreshCw size={14} className={fetching ? "provider-spinning" : undefined} />{t(fetching ? "settings.fetchingModels" : "settings.fetchModels")}</button>
      <button type="button" className="btn btn--secondary btn--small" disabled={disabled} onClick={() => openEditor()}><Plus size={14} />{t("providerUI.manualAdd")}</button>
    </div></div>
    {!editor && error && <p role="alert" className="provider-fetch-status provider-fetch-status--warn">{error}</p>}
    {!provider.models.length && <p className="provider-models-editor__empty">{t("providerUI.emptyModels")}</p>}
    <div className="provider-models-editor__list">
      {provider.models.map((model) => {
        const override = overrides.find((item) => item.model === matchingModelKey(overrides.map((item) => item.model), model ?? ""));
        const result = tests[model];
        const modelEfforts = effortLevelsFor(model);
        return <div className="provider-model-row" key={model}>
          <div className="provider-model-row__main"><span className="provider-model-row__name">{model}</span>
            <span className="badge badge--neutral" title={t("providerUI.context")}>{override?.contextWindow ? override.contextWindow.toLocaleString() : t("providerUI.auto")}</span>
            <span className="badge badge--neutral" title={t(override?.supportedEfforts?.length ? "settings.modelEffortLevelsCustom" : "settings.modelEffortLevelsInheritTitle")}>{modelEfforts.length ? modelEfforts.join("|") : t("providerUI.auto")}</span>
            <span className="badge badge--neutral">{t(vision.includes(model) ? "providerUI.image" : "providerUI.text")}</span>
            <button type="button" className="btn btn--small" aria-label={`${t("providerUI.test")} ${model}`} title={t("providerUI.test")} disabled={disabled || !canFetch || result?.busy} onClick={() => void test(model)}><Plug size={14} /></button>
            {/* tl 1104: 图标/tooltip 与上区 ProviderEditorModelPicker 对齐——
                SlidersHorizontal 17 + 「模型设置」，弹窗为 ProviderModelDialog。 */}
            <button type="button" className="btn provider-icon-action" title={t("settings.models.options")} aria-label={`${t("settings.models.options")}: ${model}`} aria-haspopup="dialog" disabled={disabled} onClick={() => openEditor(model)}><SlidersHorizontal size={17}/></button>
            <button type="button" className="btn btn--small" aria-label={`${t("common.delete")} ${model}`} title={t("common.delete")} disabled={disabled} onClick={() => onChange(provider.models.filter((name) => name !== model), overrides.filter((item) => item.model !== model), capabilities.filter((item) => item.model !== model))}><Trash2 size={14} /></button>
          </div>
          <ModelImageInputControl model={model} baseURL={provider.baseUrl} capability={modelCapabilityForModel(capabilities, model)} mode={imageInputModeForModel(imageInputModes(overrides), model)} disabled={disabled}
            onChange={(mode) => onChange(provider.models, mergeImageInputModes(overrides, provider.models, { ...imageInputModes(overrides), [model]: mode }), capabilities)} />
          {result && <div role="status" className={`provider-fetch-status provider-fetch-status--${result.error ? "warn" : "ok"}`}>{result.busy ? t("providerUI.testing") : result.error || (result.probe?.ttftMs
              ? (result.probe.tps
                ? (result.probe.outputTokens
                  ? t("providerUI.testTimingFull", { ttft: result.probe.ttftMs, tps: result.probe.tps.toFixed(1), tokens: result.probe.outputTokens, gen: result.probe.generationMs ?? 0 })
                  : t("providerUI.testTiming", { ttft: result.probe.ttftMs, tps: result.probe.tps.toFixed(1) }))
                : t("providerUI.testTimingTTFT", { ttft: result.probe.ttftMs }))
              : result.probe
                ? t("providerUI.testNoTiming")
                : t("providerUI.testSuccess"))}</div>}
        </div>;
      })}
    </div>
    {editor && <Suspense fallback={null}><ProviderModelDialog
      baseURL={provider.baseUrl} candidates={provider.models} contextDefault={provider.contextWindow || undefined}
      initial={editor.original ? { model: editor.original, contextWindow: contextWindowOf(editor.original), maxOutputTokens: outputTokensOf(editor.original), vision: visionOf(editor.original), audio: audioOf(editor.original), highSpeed: (provider.highSpeedModels ?? []).includes(editor.original), efforts: supportedEffortsOf(editor.original) } : undefined}
      // Task 1053: 弹窗内 effort 编辑区的继承基准——连接级词表（模型级未声明时
      // 沿用它，与后端 model > connection > auto 的优先级一致）。
      connectionEfforts={connectionLevels}
      // 高速模型是上区 318.1 实验开关（318.1 off 时整个条目隐藏）维护的标记；下区没有
      // 那个开关，关掉本弹窗的高速入口避免出现「能勾、但存不到连接草稿」的假开关。
      highSpeedSwitchOn={false}
      capability={editor.original ? modelCapabilityForModel(capabilities, editor.original) : undefined}
      busy={disabled}
      onClose={() => { setEditor(null); setError(null); }}
      onApply={applyModel} />
    </Suspense>}
    {discovery && <ProviderDialog title={t("settings.fetchModels")} onClose={() => setDiscovery(null)}>
      <label className="provider-catalog-search"><Search size={15} /><input className="mem-input" placeholder={t("settings.modelCandidateSearch")} value={query} onChange={(e) => setQuery(e.target.value)} /></label>
      <div className="provider-discovery-list">{filteredDiscovery.map((item) => <label key={item.model} className="provider-discovery-row"><input type="checkbox" disabled={provider.models.includes(item.model)} checked={provider.models.includes(item.model) || selection.includes(item.model)} onChange={(e) => setSelection((prev) => e.target.checked ? [...prev, item.model] : prev.filter((name) => name !== item.model))} /><span>{item.model}</span>{provider.models.includes(item.model) && <small>{t("providerUI.alreadyAdded")}</small>}</label>)}{!filteredDiscovery.length && <p>{t(discovery.length ? "settings.noMatchingCandidateModels" : "providerUI.noDiscoveredModels")}</p>}</div>
      <p className="mem-hint">{t("providerUI.discoveryHint")}</p>
      <footer><span>{t("providerUI.selected", { n: selection.length })}</span><button className="btn btn--small" onClick={() => setDiscovery(null)}>{t("common.cancel")}</button><button className="btn btn--primary btn--small" disabled={disabled || !selection.length} onClick={() => {
        const added = selection.filter((name) => !provider.models.includes(name));
        onChange([...provider.models, ...added], overrides, [...capabilities.filter((item) => !added.includes(item.model)), ...discovery.filter((item) => added.includes(item.model))]);
        setDiscovery(null);
      }}>{t("providerUI.addSelected")}</button></footer>
    </ProviderDialog>}
  </section>;
}
