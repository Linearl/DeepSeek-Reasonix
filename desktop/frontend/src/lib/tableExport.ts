// tableExport — pure text builders behind the per-table copy/download buttons
// (tl1596, learned from zcode markdown-table.tsx:83/111).
//
// Two correctness details are non-negotiable and carried over verbatim from the
// zcode reference (see the report tl1581-…-表格按钮组专项-20261114.md §1.1):
//   1. Formula-injection guard: assistant markdown is untrusted input, and
//      Excel/Numbers/LibreOffice interpret leading =+-@ in CSV cells as
//      formulas. Formula-shaped cells are demoted to plain text with a leading
//      apostrophe before download (markdown-table.tsx:81-91).
//   2. UTF-8 BOM: Blob's charset never reaches the file bytes, so Excel parses
//      a BOM-less UTF-8 CSV in the local code page and garbles Chinese. The
//      payload is prefixed with U+FEFF so spreadsheet apps detect the encoding
//      (markdown-table.tsx:109-114).
// Kept React-free so unit tests run without a DOM harness.

export type TableExportData = {
  header: string[];
  rows: string[][];
};

export function normalizeTableCellText(value: string): string {
  // Collapse horizontal whitespace for stable output, but keep "\n" line breaks
  // (from <br> elements) so the Markdown rebuild can restore them as <br>.
  return value
    .replace(/[^\S\n]+/g, " ")
    .replace(/\n{2,}/g, "\n")
    .replace(/ *\n */g, "\n")
    .trim();
}

export function escapeMarkdownTableCell(value: string): string {
  // Pipe cells must survive a GFM re-parse: backslash first, then the pipe,
  // then hard breaks become <br> (mirrors zcode markdown-table.tsx:77-79).
  return value.replace(/\\/g, "\\\\").replace(/\|/g, "\\|").replace(/\r?\n/g, "<br>");
}

export function escapeCsvCell(value: string): string {
  // Assistant markdown 属于不可信输入，CSV 被 Excel/Numbers/LibreOffice
  // 打开时会解释公式前缀。下载前统一把公式型单元格降级成纯文本。
  const safeValue = /^[\t\r\n]/u.test(value) || /^[\s]*[=+\-@]/u.test(value) ? `'${value}` : value;

  if (!/[",\r\n]/u.test(safeValue)) {
    return safeValue;
  }

  return `"${safeValue.replace(/"/g, '""')}"`;
}

export function buildMarkdownTableText(data: TableExportData): string {
  const tableRows = [data.header, ...data.rows].filter((row) => row.length > 0);
  if (tableRows.length === 0) {
    return "";
  }

  const columnCount = Math.max(...tableRows.map((row) => row.length), 1);
  const normalizedRows = tableRows.map((row) =>
    Array.from({ length: columnCount }, (_, index) => escapeMarkdownTableCell(row[index] ?? "")),
  );
  const head = normalizedRows[0] ?? [];
  const separator = Array.from({ length: columnCount }, () => "---");
  const body = normalizedRows.slice(1);

  return [head, separator, ...body].map((row) => `| ${row.join(" | ")} |`).join("\n");
}

export function buildCsvTableText(data: TableExportData): string {
  const tableRows = [data.header, ...data.rows];
  const csv = tableRows
    .map((row) => row.map((cell) => escapeCsvCell(cell)).join(","))
    .join("\r\n");
  // Blob 的 charset 不会写入文件字节，Excel 会把无 BOM 的 UTF-8 CSV
  // 按本地代码页解析，导致中文乱码；显式添加 UTF-8 BOM 让表格软件可靠识别编码。
  return `\uFEFF${csv}`;
}

/** True when there is at least a header or a body row to copy. */
export function hasExportableRows(data: TableExportData): boolean {
  return data.header.length > 0 || data.rows.length > 0;
}
