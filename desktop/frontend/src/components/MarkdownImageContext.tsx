import { createContext } from "react";

export const MarkdownImageTabContext = createContext("");

/**
 * tl1568: optional viewer opener for markdown images. The Transcript root
 * provides it (reusing the ImageViewer overlay — the same mechanism
 * MessageAttachments uses since tl1289); MarkdownImage calls it on click
 * only when provided, so hosts without a viewer (workspace preview etc.)
 * keep images non-interactive and see zero behavior change.
 */
export const MarkdownImageViewerContext = createContext<((url: string, name?: string) => void) | null>(null);
