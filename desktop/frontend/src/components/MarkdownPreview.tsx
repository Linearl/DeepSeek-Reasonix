// 任务 944：产物卡片的 markdown 预览包装。复用现有 MarkdownRenderer（同一
// remark/rehype 管线），懒加载隔离 katex/语法高亮 chunk——没展开预览的卡片
// 不为它付加载成本。输入是工具侧已界内截断的预览文本（≤40 行 / 8KB），
// 不是全文件，因此无需再套流式/虚拟化层。
import { Suspense, lazy } from "react";

const MarkdownRenderer = lazy(() => import("./MarkdownRenderer"));

export function MarkdownPreview({ text }: { text: string }) {
  return (
    <Suspense fallback={<div className="artifact-card__preview-fallback" />}>
      <MarkdownRenderer text={text} />
    </Suspense>
  );
}
