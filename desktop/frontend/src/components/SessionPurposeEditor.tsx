// 任务 938：会话职责编辑弹窗（侧栏右键「编辑职责」）。
//
// 职责（purpose）是会话在通讯录登记的真实分工——侧栏标题多为自动命名，purpose
// 才回答「这个会话是干什么的」。本弹窗补 UI 入口：多行文本编辑，保存走
// SetSessionPurpose 既有写链（agent.SetSessionDuty presence 语义：只改自由文本，
// task-348 结构化 identity/duties 字段不动）。
//
// 形态：AnchoredPopover portal 到 body（529 先例——弹层勿用 absolute 挂在有
// overflow 的宿主内，侧栏滚动容器会裁剪它）。锚点=右键的那一行；菜单关闭后
// 行元素仍在，锚定关系不丢。
//
// 状态机（验收②三态）：打开=拉当前值（缓存命中即显）；保存中禁用按钮；
// 保存失败错误行原样呈现（587 纪律：失败绝不静默吞掉）；取消/Esc/外点=关闭。

import { useCallback, useEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import { Loader2 } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { AnchoredPopover } from "./AnchoredPopover";

export type SessionPurposeEditorBindings = {
  GetSessionPurpose(sessionPath: string): Promise<string>;
  SetSessionPurpose(sessionPath: string, purpose: string): Promise<void>;
};

export function SessionPurposeEditor({
  open,
  anchorRef,
  sessionPath,
  initialPurpose,
  onClose,
  onSaved,
  bindings,
}: {
  open: boolean;
  anchorRef: RefObject<HTMLElement | null>;
  sessionPath: string;
  /** 已缓存的职责（hover 链路拉过即传入，弹窗免一次往返）；undefined=未知，打开时拉。 */
  initialPurpose?: string;
  onClose: () => void;
  /** 保存成功回调——调用方负责把新职责写进 hover 缓存（即时反映验收③）。 */
  onSaved: (purpose: string) => void;
  bindings?: SessionPurposeEditorBindings;
}) {
  const t = useT();
  const b: SessionPurposeEditorBindings = bindings ?? app;
  const [draft, setDraft] = useState(initialPurpose ?? "");
  const [loading, setLoading] = useState(initialPurpose === undefined);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const latestInitialRef = useRef(initialPurpose);
  // 每次渲染同步最新 prop：hover 链路晚到的缓存也能喂给下一次打开。
  latestInitialRef.current = initialPurpose;

  // 每次打开重置为当前值：缓存命中即显，未命中拉一次（失败不拦编辑——
  // 用户仍可直接输入新职责，保存链路自会校验）。
  useEffect(() => {
    if (!open) return;
    let alive = true;
    setError(null);
    setSaving(false);
    if (latestInitialRef.current !== undefined) {
      setDraft(latestInitialRef.current);
      setLoading(false);
      return;
    }
    setDraft("");
    setLoading(true);
    b.GetSessionPurpose(sessionPath)
      .then((purpose) => { if (alive) setDraft(purpose); })
      .catch((err) => { if (alive) setError(String(err)); })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [b, open, sessionPath]);

  const save = useCallback(async () => {
    if (saving) return;
    setSaving(true);
    setError(null);
    try {
      await b.SetSessionPurpose(sessionPath, draft);
      onSaved(draft.trim());
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  }, [b, draft, onSaved, saving, sessionPath]);

  return (
    <AnchoredPopover
      open={open}
      anchorRef={anchorRef}
      onClose={onClose}
      className="session-purpose-editor"
      placement="bottom"
    >
      <div className="session-purpose-editor__head">
        <span className="session-purpose-editor__title">{t("projectTree.editPurpose")}</span>
        <span className="session-purpose-editor__hint">{t("projectTree.purposeHint")}</span>
      </div>
      <textarea
        className="session-purpose-editor__input"
        value={draft}
        rows={12}
        placeholder={t("projectTree.purposePlaceholder")}
        aria-label={t("projectTree.editPurpose")}
        disabled={loading || saving}
        onChange={(event) => setDraft(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            void save();
          }
        }}
      />
      {error && (
        <div className="session-purpose-editor__error" role="alert">
          {t("projectTree.purposeSaveFailed")}：{error}
        </div>
      )}
      <div className="session-purpose-editor__actions">
        <button type="button" className="btn btn--secondary btn--small" onClick={onClose} disabled={saving}>
          {t("common.cancel")}
        </button>
        <button type="button" className="btn btn--small" onClick={() => void save()} disabled={saving || loading}>
          {saving ? <Loader2 size={12} className="session-purpose-editor__spinner" aria-hidden="true" /> : null}
          <span>{t("common.save")}</span>
        </button>
      </div>
    </AnchoredPopover>
  );
}
