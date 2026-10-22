import { Component, Fragment, useCallback, useEffect, useState, type ReactNode } from "react";
import { Copy, Check } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { RecoveryCopiesSection } from "./RecoveryCopiesSection";
import { SessionVersionsSection } from "./SessionVersionsSection";

type StorageSettingsView = Awaited<ReturnType<typeof app.StorageSettings>>;

/** SectionBoundary keeps one storage page section's crash local (任务 1039r).
 *
 * The settings surface boundary one level up is fail-closed by design: any
 * throw anywhere in the settings tree used to replace the whole page - path
 * fields, both panels and everything they had loaded - with a dead-end
 * 「设置加载失败。重试」 (the 1039 face, seen again in the 0715 retest). No
 * bridge rejection can land there (every expand path catches its own promises),
 * so the survivor of such a crash is only this local bound: the failing
 * section renders a reason banner with a retry that remounts just that
 * section, and the rest of the page - including the untouched section's state
 * - stays exactly as the user left it.
 */
export class SectionBoundary extends Component<
  { children: ReactNode },
  { failed: boolean; reason: string; attempt: number }
> {
  state = { failed: false, reason: "", attempt: 0 };

  static getDerivedStateFromError(error: unknown) {
    return { failed: true, reason: error instanceof Error ? error.message : String(error) };
  }

  render() {
    if (this.state.failed) {
      return (
        <SectionFailure
          reason={this.state.reason}
          onRetry={() =>
            this.setState((current) => ({ failed: false, reason: "", attempt: current.attempt + 1 }))
          }
        />
      );
    }
    // Keyed so a retry remounts the section from scratch: a crashed subtree may
    // have failed mid-load, and a clean mount is the honest retry.
    return <Fragment key={this.state.attempt}>{this.props.children}</Fragment>;
  }
}

function SectionFailure({ reason, onRetry }: { reason: string; onRetry: () => void }) {
  const t = useT();
  return (
    <div className="banner banner--error" role="alert" data-testid="settings-section-failure">
      <span>{t("settings.loadFailed")}</span>
      {/* 原因随横幅走（1039 首轮教训）：只有死话时用户与日志都定位不到断点。 */}
      <span className="rc-failed-reason">{reason}</span>
      <button className="btn btn--secondary btn--small" type="button" onClick={onRetry}>
        {t("common.retry")}
      </button>
    </div>
  );
}

export function StorageSettingsPage() {
  const t = useT();
  const [view, setView] = useState<StorageSettingsView | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setFailed(false);
    try {
      setView(await app.StorageSettings());
    } catch {
      setView(null);
      setFailed(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (loading) return <div className="empty">{t("settings.loading")}</div>;
  if (failed || !view) {
    return (
      <div className="banner banner--error settings-load-error" role="alert">
        <span>{t("settings.loadFailed")}</span>
        <button className="btn btn--secondary btn--small" type="button" onClick={() => void load()}>{t("common.retry")}</button>
      </div>
    );
  }

  return (
    <section className="settings-section">
      <div className="settings-section__head">
        <div>
          <div className="settings-section__title">{t("settings.storageTitle")}</div>
          <div className="settings-section__desc">{t("settings.storageHint")}</div>
        </div>
      </div>
      <div className="settings-section__body">
        <StoragePathField label={t("settings.defaultWorkspace")} hint={t("settings.defaultWorkspaceHint")} value={view.defaultWorkspace} />
        <StoragePathField label={t("settings.storageState")} value={view.statePath} />
        <StoragePathField label={t("settings.storageCache")} value={view.cachePath} />
        <StoragePathField label={t("settings.storageExtensions")} value={view.extensionsPath} />
      </div>
        {/* 任务 1039r：两个分区各自局部兜底——分区渲染抛错降级为该分区的
            原因横幅+重试，不再上抛给设置 surface 的整页 fail-closed 边界
            （「设置加载失败。重试」吞掉路径区与另一分区的实测形态）。 */}
        <SectionBoundary>
          <RecoveryCopiesSection />
        </SectionBoundary>
        {/* 任务 1011 需求 E：v3 events heads 的版本与修复分区，与 legacy 副本扫描分区并存 */}
        <SectionBoundary>
          <SessionVersionsSection />
        </SectionBoundary>
</section>
  );
}

function StoragePathField({ label, hint, value }: { label: ReactNode; hint?: ReactNode; value: string }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState(false);
  return (
    <div className="settings-field">
      <div className="settings-field__copy">
        <div className="settings-field__label">{label}</div>
        {hint && <div className="settings-field__hint"><span className="settings-field__hint-line">{hint}</span></div>}
      </div>
      <div className="settings-field__control settings-path-value">
        <input className="mem-input" value={value} placeholder={t("common.none")} aria-label={String(label)} readOnly />
        <button className="btn settings-icon-button" disabled={!value} title={t(copied ? "richLink.copied" : "common.copy")} aria-label={`${t("common.copy")}: ${String(label)}`} onClick={async () => { try { await navigator.clipboard.writeText(value); setCopied(true); setCopyError(false); } catch { setCopyError(true); } }}>{copied ? <Check size={16} /> : <Copy size={16} />}</button>
        {copyError && <span role="alert">{t("settings.hooksJsonClipboardUnavailable")}</span>}
      </div>
    </div>
  );
}
