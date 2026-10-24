import { useCallback, useEffect, useState } from "react";
import { FileText, Folder, Image } from "lucide-react";
import { app } from "../lib/bridge";
import { sortDisplayAttachments, type DisplayAttachment } from "../lib/attachmentDisplay";
import { useT } from "../lib/i18n";
import { ImageViewer } from "./ImageViewer";
import { Tooltip } from "./Tooltip";

// 任务1121: 图片缩略图升级为独立大图块（.msg-attachment-gallery），不再与
// 文字/文件卡混排——文字块与图块上下分离（1091 steer 口径延伸到用户消息
// 全场景）；窄容器靠 .msg-attachment-gallery img 的 max-width 约束防「图挤
// 正文成竖排单字」（容器再窄，图也是块级 max-width:100% 缩放，不 inline
// 挤压文字流）。
export function MessageImageGallery({ images, previews, onOpenViewer }: {
  images: DisplayAttachment[];
  previews: Record<string, string>;
  onOpenViewer: (attachment: DisplayAttachment) => void;
}) {
  const t = useT();
  if (images.length === 0) return null;
  return (
    <div className="msg-attachment-gallery" aria-label={t("msg.images")} data-transcript-selection-ignore>
      {images.map((attachment) => (
        <Tooltip key={attachment.path} label={`${t("imageViewer.clickToPreview")} — ${attachment.name}`} block>
          <button
            type="button"
            className="msg-attachment-gallery__item"
            onClick={() => onOpenViewer(attachment)}
          >
            <img src={previews[attachment.path] ?? ""} alt={attachment.name} draggable={false} />
          </button>
        </Tooltip>
      ))}
    </div>
  );
}

function loadPreview(tabId: string, attachment: DisplayAttachment): Promise<string> {
  const { path } = attachment;
  if (path.startsWith("draft:") && typeof app.ReadDraftImageForTab === "function") {
    return app.ReadDraftImageForTab(tabId, path.slice("draft:".length));
  }
  if (path.startsWith("attachment:") && typeof app.ReadSessionAttachmentForTab === "function") {
    const digest = path.slice("attachment:".length).slice(0, 64);
    // Task 234: this fork's DisplayAttachment carries no `mime` field — derive
    // it from the extension exactly like the fallback below did.
    const mime = attachment.ext === "JPG" || attachment.ext === "JPEG" ? "image/jpeg" : `image/${attachment.ext.toLowerCase() || "png"}`;
    return import("../lib/sessionAttachmentRead").then(module => module.readSessionAttachmentDataURL(
      tabId,
      digest,
      mime,
      (id, nextDigest, offset) => app.ReadSessionAttachmentForTab!(id, nextDigest, offset),
    ));
  }
  // Task 234: per-tab reader is optional on this fork; fall back to the
  // workspace-scoped data URL the composer already uses.
  if (typeof app.AttachmentDataURLForTab === "function") return app.AttachmentDataURLForTab(tabId, path);
  return app.AttachmentDataURL(path);
}

function attachmentIcon(kind: DisplayAttachment["kind"]) {
  if (kind === "image") return <Image size={15} />;
  if (kind === "folder") return <Folder size={15} />;
  return <FileText size={15} />;
}
// 任务1121: 1091 旧缩略图形态的点击语义（整卡可点打开 ImageViewer + 键盘
// Enter/Space）整体删除——图片改走 MessageImageGallery 独立大图块，文件卡
// 不可点（无点击语义，纯展示）。CSS 侧 .msg-attachment--image / .msg-attachment__icon--image
// 两条死规则一并删除（无 JSX 引用）。

export function MessageAttachments({ attachments, tabId = "" }: {
  attachments: DisplayAttachment[];
  tabId?: string;
}) {
  const t = useT();
  const ordered = sortDisplayAttachments(attachments);
  const [previews, setPreviews] = useState<Record<string, string>>({});
  const [viewer, setViewer] = useState({ open: false, url: "", name: "" });
  const openViewer = useCallback(async (attachment: DisplayAttachment) => {
    let url = previews[attachment.path];
    if (!url) {
      try {
        url = await loadPreview(tabId, attachment);
        setPreviews(previous => previous[attachment.path] ? previous : { ...previous, [attachment.path]: url });
      } catch { return; }
    }
    setViewer({ open: true, url, name: attachment.name });
  }, [previews, tabId]);

  const files = ordered.filter(item => item.kind !== "image");

  const previewKey = ordered.filter(item => item.kind === "image" && item.source === "attachment").map(item => item.path).join("\n");
  useEffect(() => {
    let cancelled = false;
    for (const path of previewKey ? previewKey.split("\n") : []) {
      if (previews[path]) continue;
      const attachment = ordered.find(item => item.path === path);
      if (!attachment) continue;
      loadPreview(tabId, attachment).then(url => {
        if (!cancelled) setPreviews(previous => previous[path] ? previous : { ...previous, [path]: url });
      }).catch(() => {});
    }
    return () => { cancelled = true; };
  }, [previewKey, tabId]);

  return (
    <>
      {/* 任务1121: 图片走独立大图块（.msg-attachment-gallery），文件卡仍走
          .msg-attachments 横排卡片——图文上下分离，不再 56px 缩略图混排。 */}
      <MessageImageGallery images={ordered.filter(item => item.kind === "image")} previews={previews} onOpenViewer={openViewer} />
      {files.length > 0 && (
        <div className="msg-attachments" aria-label={t("msg.attachments")} data-transcript-selection-ignore>
          {files.map((attachment, index) => (
            <div
              className={`msg-attachment msg-attachment--${attachment.kind}`}
              key={`${attachment.path}:${index}`}
              title={attachment.path}
            >
              <span className={`msg-attachment__icon msg-attachment__icon--${attachment.kind}`} aria-hidden="true">
                {attachmentIcon(attachment.kind)}
              </span>
              <span className="msg-attachment__main">
                <span className="msg-attachment__name">{attachment.name}</span>
                <span className="msg-attachment__meta">
                  {attachment.kind === "folder"
                    ? t("msg.folderReference")
                    : `${attachment.ext || t("msg.fileAttachment")} · ${attachment.source === "workspace" ? t("msg.workspaceReference") : t("msg.fileAttachment")}`}
                </span>
              </span>
            </div>
          ))}
        </div>
      )}
      <ImageViewer open={viewer.open} imageUrl={viewer.url} imageName={viewer.name} onClose={() => setViewer(value => value.open ? { ...value, open: false } : value)} />
    </>
  );
}
