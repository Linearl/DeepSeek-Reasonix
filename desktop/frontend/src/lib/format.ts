/**
 * Shared formatting utilities for token counts, byte sizes and numeric displays.
 *
 * Centralises the previously duplicated fmtTokens / fmtFullTokens helpers
 * scattered across ContextPanel and Composer so every surface renders
 * identical compact token strings (e.g. "1.5K", "2.0M"), and — since tl1618 —
 * the byte-size formatters previously duplicated across seven components.
 */

/** Options controlling how {@link formatTokens} renders a count. */
export interface TokenFormatOptions {
  /**
   * When `true` (the default) large values are abbreviated with K / M suffixes.
   * When `false` the raw locale-formatted number is returned instead.
   */
  compact?: boolean;

  /** Maximum fractional digits kept in the abbreviated form (default `1`). */
  decimals?: number;

  /**
   * Minimum value before the K-suffix kicks in (default `1000`).
   * Useful for contexts that want to show plain numbers up to a higher bound.
   */
  threshold?: number;
}

/** Strip trailing zeros after the decimal point: "1.50" → "1.5", "1.00" → "1". */
function stripTrailingZeros(s: string): string {
  return s.replace(/(\.\d*?)0+$/, "$1").replace(/\.$/, "");
}

/**
 * Format a token count for human-readable display.
 *
 * Behaviour:
 *  - Returns `"-"` for missing, zero, or negative inputs.
 *  - In compact mode (default): values >= 1 000 000 use the `M` suffix,
 *    values >= `threshold` (default 1 000) use the `K` suffix, and smaller
 *    values are rendered as-is.
 *  - Trailing `.0` is stripped so `1000` becomes `"1K"` rather than `"1.0K"`.
 *  - In non-compact mode `toLocaleString()` is used for locale-aware grouping.
 *
 * @example
 *  formatTokens(1500)        // "1.5K"
 *  formatTokens(1000)        // "1K"
 *  formatTokens(142000)      // "142K"
 *  formatTokens(1_500_000)   // "1.5M"
 *  formatTokens(999)         // "999"
 *  formatTokens(undefined)   // "-"
 */
export function formatTokens(tokens: number | undefined, options?: TokenFormatOptions): string {
  if (typeof tokens !== "number" || tokens <= 0) return "-";

  const { compact = true, decimals = 1, threshold = 1000 } = options ?? {};

  if (!compact) {
    return tokens.toLocaleString();
  }

  if (tokens >= 1_000_000) {
    return `${stripTrailingZeros((tokens / 1_000_000).toFixed(decimals))}M`;
  }
  if (tokens >= threshold) {
    return `${stripTrailingZeros((tokens / 1000).toFixed(decimals))}K`;
  }

  return String(tokens);
}

/**
 * Convenience wrapper for optional token counts (session / turn totals).
 *
 * Delegates to {@link formatTokens} and returns `"-"` when the value is
 * absent, zero, or negative — matching the old `fmtOptionalTokens` helper
 * it replaces.
 */
export function formatOptionalTokens(tokens?: number | null, options?: TokenFormatOptions): string {
  if (typeof tokens !== "number" || tokens <= 0) return "-";
  return formatTokens(tokens, options);
}

/**
 * Format a byte count for human-readable display.
 *
 * Single source for every byte-size surface (perf memory legend, recovery
 * copies, resource panel, session events, remote wizard file list, remote
 * legacy-data summary, session monitor budgets, DAG migration/cleanup cards) —
 * replaces the scattered local implementations consolidated by tl1618.
 *
 * Behaviour (the shape shared by the four identical implementations it
 * replaces, extended with the KiB-label variants' edge cases):
 *  - Returns `"—"` for missing, non-finite, zero, or negative inputs
 *    (unifying the wizard's `""` and the legacy formatter's `"0 B"`).
 *  - Binary units (÷1024, identical arithmetic in every previous copy) with
 *    KB-style labels per the 2026-11-16 user ruling — same convention as
 *    Windows Explorer / VS Code / Chrome. Numbers are unchanged; only the
 *    KiB/MiB/GiB spelling goes away.
 *  - Whole bytes and values ≥ 100 of the chosen unit render as integers;
 *    smaller values keep one decimal ("1.5 KB"). The `i === 0` exemption
 *    keeps byte-level values integral: 500 → "500 B", never "500.0 B".
 *  - Units cap at GB (values ≥ 1024 GB render as e.g. "2048 GB"), matching
 *    the four original implementations.
 *
 * @example
 *  formatBytes(0)             // "—"
 *  formatBytes(500)           // "500 B"
 *  formatBytes(1536)          // "1.5 KB"
 *  formatBytes(1024 * 100)    // "100 KB"  (≥ 100 of the unit → rounded)
 *  formatBytes(1_572_864)     // "1.5 MB"
 *  formatBytes(5 * 1024 ** 3) // "5.0 GB"
 *  formatBytes(undefined)     // "—"
 */
export function formatBytes(n: number | undefined): string {
  if (typeof n !== "number" || !Number.isFinite(n) || n <= 0) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}
