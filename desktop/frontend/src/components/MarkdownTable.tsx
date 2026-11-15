// MarkdownTable — components-map override for markdown tables.
//
// Small tables stay in document flow (horizontal overflow only).
// Large tables default to a short in-flow preview + "Expand all", so the
// trackpad never latches onto a nested vertical scroller while reading.
// Expanding mounts a bounded virtual scroller (with nested-scroll handoff)
// for thousand-row dumps without monopolizing a frame.

import {
  Children,
  cloneElement,
  isValidElement,
  memo,
  useEffect,
  useState,
  useRef,
  type CSSProperties,
  type ReactElement,
  type ReactNode,
} from "react";
import { Check, Copy, Download } from "lucide-react";
import { useVirtualizer } from "@tanstack/react-virtual";
import type { MarkdownTableAlignment, VirtualMarkdownTableData } from "../lib/largeMarkdownTable";
import {
  buildCsvTableText,
  buildMarkdownTableText,
  hasExportableRows,
  normalizeTableCellText,
  type TableExportData,
} from "../lib/tableExport";
import { t } from "../lib/i18n";
import { useToast } from "../lib/toast";
import { writeClipboardText } from "./CopyButton";
import { useTranscriptUserResizeIntent } from "./TranscriptLayoutIntentContext";

export const MARKDOWN_TABLE_VIRTUAL_MIN_ROWS = 50;
/** How many body rows to show in the default collapsed preview. */
export const MARKDOWN_TABLE_PREVIEW_ROWS = 12;
const VIRTUAL_TABLE_MAX_HEIGHT = 480;
const ESTIMATED_ROW_HEIGHT = 36;
const VIRTUAL_TABLE_OVERSCAN = 12;

function findTablePart(children: ReactNode, tag: "thead" | "tbody"): ReactElement | null {
  for (const child of Children.toArray(children)) {
    if (isValidElement(child) && child.type === tag) return child as ReactElement;
  }
  return null;
}

function tableRows(tbody: ReactElement | null): ReactElement[] {
  if (!tbody) return [];
  const children = (tbody.props as { children?: ReactNode }).children;
  return Children.toArray(children).filter(
    (child): child is ReactElement => isValidElement(child) && child.type === "tr",
  );
}

function reactNodeText(node: ReactNode): string {
  if (node == null || typeof node === "boolean") return "";
  if (typeof node === "string") return node;
  if (typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(reactNodeText).join("");
  if (isValidElement(node)) {
    // <br> is the markdown hard-break inside a table cell; keep it as "\n" so
    // the Markdown rebuild can restore it as <br> (see tableExport.ts).
    if (node.type === "br") return "\n";
    const children = (node.props as { children?: ReactNode }).children;
    return reactNodeText(children);
  }
  return "";
}

function rowCellTexts(row: ReactElement): string[] {
  const children = (row.props as { children?: ReactNode }).children;
  return Children.toArray(children)
    .filter(
      (child): child is ReactElement =>
        isValidElement(child) && (child.type === "td" || child.type === "th"),
    )
    .map((cell) => normalizeTableCellText(reactNodeText((cell.props as { children?: ReactNode }).children)));
}

function tableExportData(head: ReactElement | null, rows: ReactElement[]): TableExportData {
  // tl1596: copy/download read the React element tree (the original data), never
  // the DOM. Virtualized bodies only mount the visible window, so a DOM read
  // would silently truncate large tables to what happens to be on screen.
  const headerRow = tableRows(head)[0];
  return {
    header: headerRow ? rowCellTexts(headerRow) : [],
    rows: rows.map(rowCellTexts),
  };
}

const CSV_DOWNLOAD_FILENAME = "table.csv";

function downloadCsvFile(data: TableExportData): boolean {
  if (
    typeof document === "undefined" ||
    typeof Blob === "undefined" ||
    typeof URL === "undefined" ||
    typeof URL.createObjectURL !== "function"
  ) {
    return false;
  }
  const blob = new Blob([buildCsvTableText(data)], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = CSV_DOWNLOAD_FILENAME;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
  return true;
}

// TableExportActions — the per-table copy/download row (tl1596). Both actions
// pull rows through the caller's getData closure so every table state (small,
// folded preview, virtualized) exports the full original table. The copy button
// reuses CopyButton's three-tier clipboard channel (writeClipboardText) but is
// drawn locally with module-level t() so MarkdownTable stays provider-free
// (same rationale as TableFoldToggle; useToast's default context is a no-op).
const COPIED_FEEDBACK_MS = 1200;

function TableExportActions({ getData }: { getData: () => TableExportData }) {
  const showToast = useToast().showToast;
  const copyable = hasExportableRows(getData());
  const [copied, setCopied] = useState(false);
  const copiedTimerRef = useRef<number | null>(null);
  useEffect(() => {
    return () => {
      if (copiedTimerRef.current != null) window.clearTimeout(copiedTimerRef.current);
    };
  }, []);
  const handleCopyMarkdown = async () => {
    try {
      await writeClipboardText(buildMarkdownTableText(getData()));
      setCopied(true);
      if (copiedTimerRef.current != null) window.clearTimeout(copiedTimerRef.current);
      copiedTimerRef.current = window.setTimeout(() => {
        setCopied(false);
        copiedTimerRef.current = null;
      }, COPIED_FEEDBACK_MS);
    } catch {
      /* clipboard unavailable across all three channels */
    }
  };
  const handleDownloadCsv = () => {
    if (!downloadCsvFile(getData())) {
      showToast(t("markdown.tableCsvUnavailable"), "warn");
    }
  };
  // Task 630 precedent: a mounted-but-empty copy entry must explain itself
  // instead of silently acting like a normal copy button.
  const copyLabel = copied
    ? t("msg.copied")
    : copyable
      ? t("markdown.tableCopyMarkdown")
      : t("markdown.tableEmpty");
  return (
    <div className="md-table-export" data-table-export="">
      <button
        type="button"
        className={copied ? "copybtn copybtn--copied" : "copybtn"}
        data-table-export="copy"
        disabled={!copyable}
        aria-label={copyLabel}
        title={copyLabel}
        onClick={() => {
          void handleCopyMarkdown();
        }}
      >
        {copied ? <Check size={13} /> : <Copy size={13} />}
      </button>
      <button
        type="button"
        className="copybtn"
        data-table-export="download"
        aria-label={t("markdown.tableDownloadCsv")}
        title={t("markdown.tableDownloadCsv")}
        onClick={handleDownloadCsv}
      >
        <Download size={13} />
      </button>
    </div>
  );
}

function TableFoldToggle({
  expanded,
  totalRows,
  onToggle,
}: {
  expanded: boolean;
  totalRows: number;
  onToggle: () => void;
}) {
  const beginUserResize = useTranscriptUserResizeIntent();
  // Module-level t() so unit tests do not need a LocaleProvider; App still
  // re-renders under LocaleProvider when the locale flips.
  return (
    <button type="button" className="md-table-fold__toggle" onClick={() => { beginUserResize(); onToggle(); }}>
      {expanded
        ? t("markdown.tableCollapse")
        : t("markdown.tableExpandAll", { n: totalRows })}
    </button>
  );
}

const VirtualMarkdownTable = memo(function VirtualMarkdownTable({
  head,
  rows,
}: {
  head: ReactElement | null;
  rows: ReactElement[];
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ESTIMATED_ROW_HEIGHT,
    overscan: VIRTUAL_TABLE_OVERSCAN,
    // First paint before layout measurement (and jsdom tests, where measured
    // rects are zero) still virtualizes against a real viewport height.
    initialRect: { width: 640, height: VIRTUAL_TABLE_MAX_HEIGHT },
  });
  return (
    <div
      ref={scrollRef}
      className="md-table-virtual"
      data-nested-scroll=""
      style={{ maxHeight: VIRTUAL_TABLE_MAX_HEIGHT }}
    >
      <table>
        {head}
        <tbody style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          {virtualizer.getVirtualItems().map((virtualRow) => {
            const row = rows[virtualRow.index];
            if (!row) return null;
            return cloneElement(row, {
              key: row.key ?? virtualRow.key,
              "data-index": virtualRow.index,
              ref: virtualizer.measureElement,
              style: {
                position: "absolute",
                top: 0,
                left: 0,
                width: "100%",
                transform: `translateY(${virtualRow.start}px)`,
              } as CSSProperties,
            } as Partial<unknown>);
          })}
        </tbody>
      </table>
    </div>
  );
});

const CollapsibleLargeMarkdownTable = memo(function CollapsibleLargeMarkdownTable({
  head,
  rows,
}: {
  head: ReactElement | null;
  rows: ReactElement[];
}) {
  const [expanded, setExpanded] = useState(false);
  const preview = rows.slice(0, MARKDOWN_TABLE_PREVIEW_ROWS);
  return (
    <div className="md-table-fold" data-expanded={expanded ? "true" : undefined}>
      <TableExportActions getData={() => tableExportData(head, rows)} />
      {expanded ? (
        <VirtualMarkdownTable head={head} rows={rows} />
      ) : (
        <div className="md-table-scroll">
          <table>
            {head}
            <tbody>
              {preview.map((row, index) =>
                cloneElement(row, { key: row.key ?? `preview-${index}` } as Partial<unknown>),
              )}
            </tbody>
          </table>
        </div>
      )}
      <TableFoldToggle
        expanded={expanded}
        totalRows={rows.length}
        onToggle={() => setExpanded((v) => !v)}
      />
    </div>
  );
});

export const MarkdownTable = memo(function MarkdownTable({ children }: { children?: ReactNode }) {
  const tbody = findTablePart(children, "tbody");
  const rows = tableRows(tbody);
  const head = findTablePart(children, "thead");
  const getData = () => tableExportData(head, rows);
  // Small tables stay in document flow. Horizontal overflow lives on a wrapper
  // (overflow-y:hidden) so CSS does not promote overflow-y to auto and steal
  // trackpad Y from the transcript.
  if (rows.length <= MARKDOWN_TABLE_VIRTUAL_MIN_ROWS) {
    return (
      <>
        <TableExportActions getData={getData} />
        <div className="md-table-scroll">
          <table>{children}</table>
        </div>
      </>
    );
  }
  return <CollapsibleLargeMarkdownTable head={head} rows={rows} />;
});

function alignmentProps(align: MarkdownTableAlignment): { align?: "left" | "center" | "right" } {
  return align ? { align } : {};
}

/** Virtual table used by the worker's conservative large plain-table path. */
export const VirtualMarkdownSourceTable = memo(function VirtualMarkdownSourceTable({
  data,
}: {
  data: VirtualMarkdownTableData;
}) {
  const [expanded, setExpanded] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const totalRows = data.rows.length;
  const isLarge = totalRows > MARKDOWN_TABLE_VIRTUAL_MIN_ROWS;
  const visibleRows = expanded || !isLarge
    ? data.rows
    : data.rows.slice(0, MARKDOWN_TABLE_PREVIEW_ROWS);

  const virtualizer = useVirtualizer({
    count: expanded && isLarge ? totalRows : 0,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ESTIMATED_ROW_HEIGHT,
    overscan: VIRTUAL_TABLE_OVERSCAN,
    initialRect: { width: 640, height: VIRTUAL_TABLE_MAX_HEIGHT },
  });

  if (!isLarge) {
    return (
      <>
        <TableExportActions getData={() => ({ header: data.header, rows: data.rows })} />
        <div className="md-table-scroll" data-markdown-source-rows={totalRows}>
          <table>
            <thead>
              <tr>
                {data.header.map((cell, index) => (
                  <th key={index} {...alignmentProps(data.align[index] ?? null)}>{cell}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {data.rows.map((row, rowIndex) => (
                <tr key={rowIndex}>
                  {row.map((cell, index) => (
                    <td key={index} {...alignmentProps(data.align[index] ?? null)}>{cell}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </>
    );
  }

  return (
    <div className="md-table-fold" data-expanded={expanded ? "true" : undefined} data-markdown-source-rows={totalRows}>
      <TableExportActions getData={() => ({ header: data.header, rows: data.rows })} />
      {expanded ? (
        <div
          ref={scrollRef}
          className="md-table-virtual"
          data-nested-scroll=""
          style={{ maxHeight: VIRTUAL_TABLE_MAX_HEIGHT }}
        >
          <table>
            <thead>
              <tr>
                {data.header.map((cell, index) => (
                  <th key={index} {...alignmentProps(data.align[index] ?? null)}>{cell}</th>
                ))}
              </tr>
            </thead>
            <tbody style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
              {virtualizer.getVirtualItems().map((virtualRow) => {
                const row = data.rows[virtualRow.index];
                if (!row) return null;
                return (
                  <tr
                    key={virtualRow.key}
                    data-index={virtualRow.index}
                    ref={virtualizer.measureElement}
                    style={{
                      position: "absolute",
                      top: 0,
                      left: 0,
                      width: "100%",
                      transform: `translateY(${virtualRow.start}px)`,
                    }}
                  >
                    {row.map((cell, index) => (
                      <td key={index} {...alignmentProps(data.align[index] ?? null)}>{cell}</td>
                    ))}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="md-table-scroll">
          <table>
            <thead>
              <tr>
                {data.header.map((cell, index) => (
                  <th key={index} {...alignmentProps(data.align[index] ?? null)}>{cell}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {visibleRows.map((row, rowIndex) => (
                <tr key={rowIndex}>
                  {row.map((cell, index) => (
                    <td key={index} {...alignmentProps(data.align[index] ?? null)}>{cell}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <TableFoldToggle
        expanded={expanded}
        totalRows={totalRows}
        onToggle={() => setExpanded((v) => !v)}
      />
    </div>
  );
});
