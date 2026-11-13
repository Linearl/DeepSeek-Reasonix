export interface ComposerContentSizing {
  inputHeight: number;
  logicalHeight: number | null;
  overflow: boolean;
}

// tl1578 修③：输入区预留高度单一来源（SSOT）。meta 行（按钮行）实测超过
// 名义高度的部分（折两行 ≈81px 比名义 43px 多出 ≈38px）必须同时喂两个消费
// 方——输入高度测量与卡片高度写手 effectiveComposerHeight。此前写手漏接仍
// 用裸基础值：resized 定高卡比真实内容矮一截（meta 折行量），卡内内容从卡
// 底下溢、盖到状态栏；卡调得越矮差值占比越大=「手动减高溢出加重」。
// jsdom/未实测（offsetHeight 恒 0）→ 增量 0 → 与裸基础值逐字节同值。
export function composerReservedHeight(baseReservedHeight: number, metaExtraHeight: number): number {
  return baseReservedHeight + Math.max(0, metaExtraHeight);
}

export function resolveComposerContentSizing({
  contentHeight,
  manualLogicalHeight,
  maxLogicalHeight,
  reservedHeight,
  minInputHeight = 32,
}: {
  contentHeight: number;
  manualLogicalHeight: number | null;
  maxLogicalHeight: number;
  reservedHeight: number;
  minInputHeight?: number;
}): ComposerContentSizing {
  const safeContentHeight = Math.max(0, contentHeight);
  const maxInputHeight = Math.max(minInputHeight, maxLogicalHeight - reservedHeight);
  const manualInputHeight = manualLogicalHeight === null
    ? 0
    : Math.max(minInputHeight, manualLogicalHeight - reservedHeight);
  const inputHeight = Math.min(Math.max(safeContentHeight, manualInputHeight), maxInputHeight);
  const logicalHeight = manualLogicalHeight === null
    ? null
    : Math.min(maxLogicalHeight, Math.max(manualLogicalHeight, inputHeight + reservedHeight));

  return {
    inputHeight,
    logicalHeight,
    overflow: safeContentHeight > maxInputHeight + 1,
  };
}
