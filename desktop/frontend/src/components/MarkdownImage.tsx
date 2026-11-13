import { useContext, useEffect, useMemo, useState } from "react";
import { app } from "../lib/bridge";
import { hasMarkdownImageResolver, markdownImageSource, type MarkdownImageView } from "../lib/markdownImage";
import { RichMarkdownLink } from "./githubLink";
import { MarkdownImageTabContext, MarkdownImageViewerContext } from "./MarkdownImageContext";

export function MarkdownImage({ src, alt, title }: { src?: string; alt?: string; title?: string }) {
  const tabId = useContext(MarkdownImageTabContext);
  const openViewer = useContext(MarkdownImageViewerContext);
  const source = src?.trim() ?? "";
  const resolverAvailable = hasMarkdownImageResolver();
  const legacyView = useMemo<MarkdownImageView>(
    () => ({ url: markdownImageSource(source), openHref: /^https?:\/\//i.test(source) ? source : undefined }),
    [source],
  );
  const [resolved, setResolved] = useState<MarkdownImageView | null>(() => resolverAvailable ? null : legacyView);
  const [loadFailed, setLoadFailed] = useState(false);

  useEffect(() => {
    let live = true;
    setLoadFailed(false);
    if (!hasMarkdownImageResolver()) {
      setResolved(legacyView);
      return () => { live = false; };
    }
    setResolved(null);
    void app.ResolveMarkdownImageForTab(tabId, source).then((view) => {
      if (live) setResolved(view);
    }).catch(() => {
      if (live) setResolved({ url: "", errorCode: "resolve-failed" });
    });
    return () => { live = false; };
  }, [legacyView, source, tabId]);

  const unavailable = loadFailed || Boolean(resolved?.errorCode) || (resolved !== null && !resolved.url);
  if (unavailable) {
    const label = alt?.trim() || resolved?.filename || "Image unavailable";
    return (
      <span className="md-image-fallback" role="img" aria-label={label} title={resolved?.errorCode || title}>
        <span>{label}</span>
        {resolved?.openHref && <RichMarkdownLink href={resolved.openHref}>Open image</RichMarkdownLink>}
      </span>
    );
  }
  if (!resolved) {
    return <span className="md-image-placeholder" role="status" aria-label={alt?.trim() || "Loading image"} />;
  }
  // tl1568: 缩放后看细节的路径——转录宿主提供了 viewer 时点击打开原图
  // （ImageViewer 弹层，附件缩略图 tl1289 同款机制）；无 viewer 宿主保持
  // 纯展示，零行为变化。键盘可达性对齐 MessageAttachments（Enter/Space）。
  const zoomable = openViewer !== null;
  return (
    <img
      src={resolved.url}
      alt={alt ?? ""}
      title={title}
      loading="lazy"
      decoding="async"
      referrerPolicy="no-referrer"
      draggable={false}
      className={zoomable ? "md-image-zoomable" : undefined}
      onClick={zoomable ? () => openViewer(resolved.url, resolved.filename ?? alt ?? undefined) : undefined}
      tabIndex={zoomable ? 0 : undefined}
      onKeyDown={zoomable ? (event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          openViewer(resolved.url, resolved.filename ?? alt ?? undefined);
        }
      } : undefined}
    />
  );
}
