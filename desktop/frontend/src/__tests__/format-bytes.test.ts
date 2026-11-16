import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { formatBytes } from "../lib/format";

// tl1618 — single-source byte formatter. Pins the unified KB-label口径
// (binary ÷1024 arithmetic, KB/MB/GB spelling per the 2026-11-16 ruling),
// the "—" invalid-input behaviour, and the byte-level integer exemption
// that fixes the wizard's "500.0 B" drift.

describe("formatBytes", () => {
  it("returns '—' for missing, non-finite, zero, and negative values", () => {
    assert.equal(formatBytes(undefined), "—");
    assert.equal(formatBytes(0), "—");
    assert.equal(formatBytes(-512), "—");
    assert.equal(formatBytes(Number.NaN), "—");
    assert.equal(formatBytes(Number.POSITIVE_INFINITY), "—");
    assert.equal(formatBytes(Number.NEGATIVE_INFINITY), "—");
  });

  it("renders byte-level values as integers (i === 0 exemption)", () => {
    assert.equal(formatBytes(1), "1 B");
    assert.equal(formatBytes(80), "80 B");
    assert.equal(formatBytes(500), "500 B");
    assert.equal(formatBytes(1023), "1023 B");
  });

  it("uses KB-style labels over binary division (1024-based numbers unchanged)", () => {
    assert.equal(formatBytes(1024), "1.0 KB");
    assert.equal(formatBytes(1536), "1.5 KB");
    assert.equal(formatBytes(1024 * 1024), "1.0 MB");
    assert.equal(formatBytes(1_572_864), "1.5 MB");
    assert.equal(formatBytes(5 * 1024 ** 3), "5.0 GB");
  });

  it("rounds values ≥ 100 of the chosen unit to integers", () => {
    assert.equal(formatBytes(99 * 1024), "99.0 KB");
    assert.equal(formatBytes(100 * 1024), "100 KB");
    assert.equal(formatBytes(149_760), "146 KB"); // 146.25 KB
    assert.equal(formatBytes(153_600), "150 KB");
  });

  it("caps units at GB", () => {
    assert.equal(formatBytes(2 * 1024 ** 4), "2048 GB");
  });

  it("keeps one decimal for fractional units below 100", () => {
    assert.equal(formatBytes(1024 + 512), "1.5 KB");
    assert.equal(formatBytes(2.5 * 1024 * 1024), "2.5 MB");
  });

  it("renders the same string the wizard and the resource panel show", () => {
    // Same-value consistency across consumer surfaces (tl1618 acceptance):
    // the wizard file list and the resource panel must agree byte-for-byte.
    assert.equal(formatBytes(500), formatBytes(500));
    assert.equal(formatBytes(500), "500 B"); // was "500.0 B" in the wizard
    assert.equal(formatBytes(2048), "2.0 KB"); // was "2.0 KiB" in the wizard
    assert.equal(formatBytes(3 * 1024 * 1024), "3.0 MB"); // was "3.0 MiB"
  });
});
