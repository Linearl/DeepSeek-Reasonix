import { useCallback, useContext, useEffect, useState } from "react";
import { Copy, FileText, Folder, FolderOpen, Image } from "lucide-react";
import { app } from "../lib/bridge";
import { isChannelAttachmentRef, sortDisplayAttachments, type DisplayAttachment } from "../lib/attachmentDisplay";
import { useT } from "../lib/i18n";
import { writeClipboardText } from "../lib/clipboard";
import { useToast } from "../lib/toast";
import { ContextMenu, contextMenuPointFromEvent, type ContextMenuItem, type ContextMenuPoint } from "./ContextMenu";
import { MarkdownImageTabContext } from "./MarkdownImageContext";
import { ImageViewer } from "./ImageViewer";
import { Tooltip } from "./Tooltip";

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
  // tl1465: cross-session borne references are absolute (channel attachments
  // anchor at the global workspace) — AttachmentDataURL's relative-only
  // contract would reject them, so route through the channel read bridge
  // (its Go side enforces the global-attachments containment boundary).
  if (isChannelAttachmentRef(path) && typeof app.ChannelAttachmentDataURL === "function") {
    return app.ChannelAttachmentDataURL(path);
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

// tl1565（G3 打开能力）：打开/复制动作的目标解析。opener 家族（任务 1060
// 同款）——ResolveOpenablePathForTab 是权威门禁：绝对路径归一化+存在性+非可
// 执行；相对路径按会话工作区解析（Global 拒绝）。返回空串=不可开（探针为真
// ⟺ OpenLocalPath 能开，保证点击必有响应、无死按钮），由调用方诚实提示。
async function resolveAttachmentTarget(attachment: DisplayAttachment, tabId: string): Promise<string> {
  try {
    return await app.ResolveOpenablePathForTab(tabId, attachment.path) || "";
  } catch {
    return "";
  }
}

// 任务 1060 同款：打开/定位失败的 toast 附原因（错误文案就地提取）。
function localPathErrorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function MessageAttachments({ attachments, tabId = "" }: {
  attachments: DisplayAttachment[];
  tabId?: string;
}) {
  const t = useT();
  const { showToast } = useToast();
  // Transcript 根上提供的会话 tab（相对路径按该会话工作区解析）；群聊流等
  // 无 Transcript 宿主的场景得空串——相对引用解析不出（探针拒绝）→ 诚实提示，
  // 绝对引用不受影响。
  const contextTabId = useContext(MarkdownImageTabContext);
  const effectiveTabId = tabId || contextTabId;
  const ordered = sortDisplayAttachments(attachments);
  const [previews, setPreviews] = useState<Record<string, string>>({});
  // tl1289: the viewer carries the message's resolved image list + start
  // index so the lightbox can navigate with arrows / ←→ keys.
  const [viewer, setViewer] = useState<{ open: boolean; images: { url: string; name: string }[]; index: number }>({ open: false, images: [], index: 0 });
  // tl1565: 非图片卡的右键菜单（复制内容 / 复制路径 / 打开所在文件夹）。
  const [menu, setMenu] = useState<{ attachment: DisplayAttachment; point: ContextMenuPoint } | null>(null);
  const openViewer = useCallback(async (attachment: DisplayAttachment) => {
    const imageAttachments = ordered.filter(item => item.kind === "image");
    const clickedIndex = imageAttachments.findIndex(item => item.path === attachment.path);
    let url = previews[attachment.path];
    if (!url) {
      try {
        url = await loadPreview(effectiveTabId, attachment);
        setPreviews(previous => previous[attachment.path] ? previous : { ...previous, [attachment.path]: url });
      } catch { return; }
    }
    // tl1289: resolve sibling image previews in parallel so the lightbox can
    // switch between all images of the message; failures drop out of the
    // list instead of blocking the clicked entry.
    const clickedUrl = url;
    const entries = await Promise.all(imageAttachments.map(async (item, position) => {
      if (position === clickedIndex) return { url: clickedUrl, name: item.name };
      const cached = previews[item.path];
      if (cached) return { url: cached, name: item.name };
      try {
        const siblingUrl = await loadPreview(effectiveTabId, item);
        return { url: siblingUrl, name: item.name };
      } catch { return null; }
    }));
    const resolved = entries.filter((entry): entry is { url: string; name: string } => entry !== null);
    const resolvedIndex = clickedIndex >= 0
      ? Math.max(0, entries.slice(0, clickedIndex).filter(entry => entry !== null).length)
      : 0;
    // Cache sibling URLs so grid thumbnails and later opens reuse them.
    setPreviews(previous => {
      const patch: Record<string, string> = {};
      for (const [position, entry] of entries.entries()) {
        if (entry && !previous[imageAttachments[position].path]) {
          patch[imageAttachments[position].path] = entry.url;
        }
      }
      return Object.keys(patch).length > 0 ? { ...previous, ...patch } : previous;
    });
    setViewer({ open: true, images: resolved, index: resolvedIndex });
  }, [ordered, previews, effectiveTabId]);

  const previewKey = ordered.filter(item => item.kind === "image" && item.source === "attachment").map(item => item.path).join("\n");
  useEffect(() => {
    let cancelled = false;
    for (const path of previewKey ? previewKey.split("\n") : []) {
      if (previews[path]) continue;
      const attachment = ordered.find(item => item.path === path);
      if (!attachment) continue;
      loadPreview(effectiveTabId, attachment).then(url => {
        if (!cancelled) setPreviews(previous => previous[path] ? previous : { ...previous, [path]: url });
      }).catch(() => {});
    }
    return () => { cancelled = true; };
  }, [previewKey, effectiveTabId]);

  // tl1565（G3）：左键=系统默认应用打开（文件卡与文件夹卡同一入口——Go 侧
  // openWorkspacePathWithType 对目录走文件管理器）。失败 toast 附原因，绝不
  // 静默；探针拒绝（不存在/Global 相对路径/可执行目标）=「路径当前无法打开」。
  const openWithDefaultApp = useCallback(async (attachment: DisplayAttachment) => {
    const target = await resolveAttachmentTarget(attachment, effectiveTabId);
    if (!target) {
      showToast(t("msg.attachmentPathUnavailable"), "error");
      return;
    }
    try {
      await app.OpenLocalPath(target);
    } catch (error) {
      showToast(t("externalOpener.failed", { name: t("externalOpener.openDefault"), error: localPathErrorText(error) }), "error");
    }
  }, [effectiveTabId, showToast, t]);

  const revealAttachment = useCallback(async (attachment: DisplayAttachment) => {
    const target = await resolveAttachmentTarget(attachment, effectiveTabId);
    if (!target) {
      showToast(t("msg.attachmentPathUnavailable"), "error");
      return;
    }
    try {
      await app.RevealPath(target);
    } catch (error) {
      showToast(t("externalOpener.failed", { name: t("externalOpener.reveal"), error: localPathErrorText(error) }), "error");
    }
  }, [effectiveTabId, showToast, t]);

  // 复制路径：解析成功给绝对路径（别处可直接用），探针失败原样复制引用本身
  // （路径字符串仍是有效信息）；写剪贴板失败 toast（与转录复制同口径）。
  const copyAttachmentPath = useCallback(async (attachment: DisplayAttachment) => {
    const resolved = await resolveAttachmentTarget(attachment, effectiveTabId);
    const copied = await writeClipboardText(resolved || attachment.path);
    if (!copied) showToast(t("diag.copyFailed"), "error");
  }, [effectiveTabId, showToast, t]);

  // 复制内容：ReadLocalTextFile 读桥（opener 门禁家族读侧延伸）——目录/二进
  // 制/超大文件诚实报错；解析探针失败同样进 toast，不静默。
  const copyAttachmentContents = useCallback(async (attachment: DisplayAttachment) => {
    const target = await resolveAttachmentTarget(attachment, effectiveTabId);
    if (!target) {
      showToast(t("msg.attachmentPathUnavailable"), "error");
      return;
    }
    try {
      const text = await app.ReadLocalTextFile(target);
      const copied = await writeClipboardText(text);
      if (!copied) showToast(t("diag.copyFailed"), "error");
    } catch (error) {
      showToast(t("externalOpener.failed", { name: t("msg.copyContents"), error: localPathErrorText(error) }), "error");
    }
  }, [effectiveTabId, showToast, t]);

  const menuItems: ContextMenuItem[] = menu ? [
    ...(menu.attachment.kind === "file" ? [{
      key: "copy-contents",
      icon: <Copy size={13} aria-hidden="true" />,
      label: t("msg.copyContents"),
      onSelect: () => { void copyAttachmentContents(menu.attachment); },
    }] : []),
    {
      key: "copy-path",
      icon: <FileText size={13} aria-hidden="true" />,
      label: t("msg.copyPath"),
      onSelect: () => { void copyAttachmentPath(menu.attachment); },
    },
    {
      key: "reveal",
      icon: <FolderOpen size={13} aria-hidden="true" />,
      label: t("externalOpener.reveal"),
      onSelect: () => { void revealAttachment(menu.attachment); },
    },
  ] : [];

  return (
    <div className="msg-attachments" aria-label={t("msg.attachments")} data-transcript-selection-ignore>
      {ordered.map((attachment, index) => {
        const image = attachment.kind === "image";
        const interactive = !image;
        const card = (
          <div
            className={`msg-attachment msg-attachment--${attachment.kind}`}
            key={image ? undefined : `${attachment.path}:${index}`}
            title={image ? undefined : attachment.path}
            onClick={image ? () => openViewer(attachment) : interactive ? () => { void openWithDefaultApp(attachment); } : undefined}
            onContextMenu={interactive ? (event) => {
              event.preventDefault();
              event.stopPropagation();
              setMenu({ attachment, point: contextMenuPointFromEvent(event) });
            } : undefined}
            role={image || interactive ? "button" : undefined}
            tabIndex={image || interactive ? 0 : undefined}
            onKeyDown={event => {
              if (event.key === "Enter" || event.key === " ") {
                event.preventDefault();
                if (image) void openViewer(attachment);
                else void openWithDefaultApp(attachment);
                return;
              }
              // 键盘菜单入口（Shift+F10 / ContextMenu 键）——与右键同点位语义。
              if (interactive && event.key === "ContextMenu") {
                event.preventDefault();
                setMenu({ attachment, point: contextMenuPointFromEvent(event) });
              }
            }}
          >
            <span className={`msg-attachment__icon msg-attachment__icon--${attachment.kind}`} aria-hidden="true">
              {image && previews[attachment.path] ? <img src={previews[attachment.path]} alt="" draggable={false} /> : attachmentIcon(attachment.kind)}
            </span>
            <span className="msg-attachment__main">
              <span className="msg-attachment__name">{attachment.name}</span>
              <span className="msg-attachment__meta">
                {attachment.kind === "folder"
                  ? t("msg.folderReference")
                  : `${attachment.ext || t("msg.fileAttachment")} · ${attachment.source === "workspace" ? t("msg.workspaceReference") : image ? t("msg.imageAttachment") : t("msg.fileAttachment")}`}
              </span>
            </span>
          </div>
        );
        return image ? <Tooltip key={`${attachment.path}:${index}`} label={t("imageViewer.clickToPreview")} block>{card}</Tooltip> : card;
      })}
      <ImageViewer
        open={viewer.open}
        imageUrl={viewer.images[viewer.index]?.url ?? ""}
        imageName={viewer.images[viewer.index]?.name}
        images={viewer.images}
        initialIndex={viewer.index}
        onClose={() => setViewer(value => value.open ? { ...value, open: false } : value)}
      />
      <ContextMenu
        open={menu != null}
        point={menu?.point ?? null}
        items={menuItems}
        onClose={() => setMenu(null)}
        ariaLabel={t("msg.attachments")}
      />
    </div>
  );
}
