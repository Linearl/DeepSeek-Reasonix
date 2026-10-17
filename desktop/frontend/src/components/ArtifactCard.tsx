// 任务 944：对话内产物卡片（render_artifact 的渲染面）。首期范围：md/text 卡
// 带折叠预览，html/pdf/image 仅「打开」不做内嵌沙箱（草图 §3.4 风险 1）。
// 打开链路全复用 opener 家族：主按钮=默认应用直开（OpenLocalPath），箭头展开
// 下拉（应用目录 OpenLocalPathInExternalOpener + 文件管理器 RevealPath（Windows
// 已带 explorer /select 选中态）+ 复制路径 + 另存为）。每一个动作失败都有
// toast 反馈（草图 §3.4 风险 4：不许 dead button）。

import { useCallback, useEffect, useMemo, useState } from "react";
import { ChevronDown, Copy, ExternalLink, FileText, FolderOpen, Save } from "lucide-react";
import { app } from "../lib/bridge";
import { writeClipboardText } from "../lib/clipboard";
import { useToast } from "../lib/toast";
import { useT } from "../lib/i18n";
import type { Item } from "../lib/useController";
import type { ExternalOpenerView, ExternalOpenersView } from "../lib/types";
import {
  artifactCardData,
  formatArtifactSize,
  type ArtifactCardData,
} from "../lib/artifactCard";
import { useArchivedToolData } from "../lib/useArchivedToolData";
import { ContextMenu, type ContextMenuItem, type ContextMenuPoint } from "./ContextMenu";
import { CodeViewer } from "./CodeViewer";
import { MarkdownPreview } from "./MarkdownPreview";

type ToolItem = Extract<Item, { kind: "tool" }>;

function localPathErrorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function kindLabelKey(kind: ArtifactCardData["kind"]): string {
  switch (kind) {
    case "md": return "artifact.kind.md";
    case "html": return "artifact.kind.html";
    case "pdf": return "artifact.kind.pdf";
    case "image": return "artifact.kind.image";
    default: return "artifact.kind.text";
  }
}

export function ArtifactCard({ item, tabId }: { item: ToolItem; tabId?: string }) {
  const t = useT();
  const { showToast } = useToast();
  // Archived tool rows drop output entirely; the full payload comes back via
  // ToolResultForTab on demand (same channel ToolCard uses).
  const archived = useArchivedToolData(item, tabId, true);
  const [menuPoint, setMenuPoint] = useState<ContextMenuPoint | null>(null);
  const [openers, setOpeners] = useState<ExternalOpenersView>({ openers: [], preferred: "" });
  const [previewOpen, setPreviewOpen] = useState(false);

  const data = useMemo(
    () => artifactCardData(item, archived.data?.output),
    [item, archived.data?.output],
  );

  const refreshOpeners = useCallback(() => {
    void app.ExternalOpeners().then((next) => {
      setOpeners({
        openers: Array.isArray(next.openers) ? next.openers : [],
        preferred: next.preferred ?? "",
      });
    }).catch(() => {});
  }, []);

  useEffect(() => { refreshOpeners(); }, [refreshOpeners]);

  const fail = useCallback((action: string, error: unknown) => {
    showToast(t("externalOpener.failed", { name: action, error: localPathErrorText(error) }), "error");
  }, [showToast, t]);

  const path = data?.path ?? "";

  const openDefault = useCallback(() => {
    void app.OpenLocalPath(path).catch((error) => fail(t("artifact.open"), error));
  }, [path, fail, t]);

  const openWith = useCallback((opener: ExternalOpenerView) => {
    setMenuPoint(null);
    void app.OpenLocalPathInExternalOpener(path, opener.id).catch((error) => fail(opener.name, error));
  }, [path, fail]);

  const reveal = useCallback(() => {
    setMenuPoint(null);
    void app.RevealPath(path).catch((error) => fail(t("externalOpener.reveal"), error));
  }, [path, fail, t]);

  const copyPath = useCallback(() => {
    setMenuPoint(null);
    void writeClipboardText(path).then((ok) => {
      if (!ok) showToast(t("externalOpener.failed", { name: t("projectTree.copyPath"), error: "clipboard" }), "error");
    });
  }, [path, showToast, t]);

  const saveAs = useCallback(() => {
    setMenuPoint(null);
    void app.SaveLocalPathAs(path).then((savedPath) => {
      if (savedPath) showToast(t("externalOpener.saved", { path: savedPath }), "info");
    }).catch((error) => fail(t("externalOpener.saveAs"), error));
  }, [path, showToast, fail, t]);

  const menuItems = useMemo<ContextMenuItem[]>(() => {
    const openerItems = openers.openers
      .filter((opener) => opener.kind !== "file-manager")
      .map((opener) => ({
        key: `open-with-${opener.id}`,
        label: t("externalOpener.openIn", { name: opener.name }),
        onSelect: () => openWith(opener),
      }));
    return [
      {
        key: "open-default",
        icon: <ExternalLink size={13} />,
        label: t("externalOpener.openDefault"),
        onSelect: () => { setMenuPoint(null); openDefault(); },
      },
      ...(openerItems.length > 0
        ? [{ type: "separator" as const, key: "open-with-separator" }, ...openerItems]
        : []),
      { type: "separator" as const, key: "path-separator" },
      { key: "reveal", icon: <FolderOpen size={13} />, label: t("externalOpener.reveal"), onSelect: reveal },
      { key: "copy-path", icon: <Copy size={13} />, label: t("projectTree.copyPath"), onSelect: copyPath },
      { key: "save-as", icon: <Save size={13} />, label: t("externalOpener.saveAs"), onSelect: saveAs },
    ];
  }, [openers.openers, openWith, openDefault, reveal, copyPath, saveAs, t]);

  if (!data) {
    // Args archived beyond JSON validity AND the archived fetch failed: the
    // card cannot even name its target. Render a minimal honest placeholder —
    // never a dead card claiming an open action it cannot perform.
    return (
      <div className="artifact-card artifact-card--empty" role="note">
        {archived.failed ? t("artifact.dataLost") : t("artifact.loading")}
      </div>
    );
  }

  const { title, kind, note, size, preview } = data;
  const hasPreview = (kind === "md" || kind === "text") && Boolean(preview);

  return (
    <div className="artifact-card" data-artifact-kind={kind}>
      <div className="artifact-card__head">
        <FileText className="artifact-card__icon" size={16} aria-hidden />
        <span className="artifact-card__title" title={title}>{title}</span>
        <span className="artifact-card__kind">{t(kindLabelKey(kind) as never)}</span>
        {typeof size === "number" && (
          <span className="artifact-card__size">{formatArtifactSize(size)}</span>
        )}
      </div>
      <div className="artifact-card__path" title={path}>{path}</div>
      {note ? <div className="artifact-card__note">{note}</div> : null}
      {archived.failed ? (
        <div className="artifact-card__warn">{t("artifact.dataLost")}</div>
      ) : null}

      {hasPreview ? (
        <div className="artifact-card__preview">
          {previewOpen ? (
            <div className="artifact-card__preview-body">
              {kind === "md"
                ? <MarkdownPreview text={preview!} />
                : <CodeViewer value={preview!} maxHeight={280} />}
            </div>
          ) : null}
          <button
            type="button"
            className="artifact-card__preview-toggle"
            onClick={() => setPreviewOpen((open) => !open)}
            aria-expanded={previewOpen}
          >
            <ChevronDown
              size={13}
              className={`artifact-card__preview-chevron${previewOpen ? " artifact-card__preview-chevron--open" : ""}`}
              aria-hidden
            />
            {t(previewOpen ? "artifact.previewHide" : "artifact.previewShow")}
            {data.previewTruncated && !previewOpen ? ` · ${t("artifact.previewTruncated")}` : ""}
          </button>
        </div>
      ) : null}

      <div className="artifact-card__actions">
        <button type="button" className="artifact-card__open" onClick={openDefault}>
          <ExternalLink size={13} aria-hidden />
          {t("artifact.open")}
        </button>
        <button
          type="button"
          className="artifact-card__menu-btn"
          aria-label={t("artifact.moreActions")}
          aria-haspopup="menu"
          onClick={(event) => {
            const rect = event.currentTarget.getBoundingClientRect();
            setMenuPoint({ left: rect.left, top: rect.bottom + 4 });
          }}
        >
          <ChevronDown size={13} aria-hidden />
        </button>
      </div>

      <ContextMenu
        open={menuPoint !== null}
        point={menuPoint}
        items={menuItems}
        onClose={() => setMenuPoint(null)}
        ariaLabel={t("artifact.moreActions")}
      />
    </div>
  );
}
