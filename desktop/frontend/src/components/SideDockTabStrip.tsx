import { useRef, useState, type DragEvent as ReactDragEvent } from "react";
import { X } from "lucide-react";
import { useT } from "../lib/i18n";
import { ContextMenu, contextMenuPointFromEvent, type ContextMenuItem, type ContextMenuPoint } from "./ContextMenu";
import { Tooltip } from "./Tooltip";
import type { SideDockTab, SideDockTabsState } from "../lib/sideDockTabs";

// tl 1108/1109 — 边栏浮动标签条（文件面板与子代理面板共用，禁复制）。
// 能力：多开 / 单击选中 / 右键关闭全部·其他·右侧 / 拖拽排序（顺序由调用方
// 持久化）/ 单标签 ×。
// 标签斜体=预览（未钉住）、正体=钉住。

type SideDockTabStripProps<TId extends string = string> = {
  state: SideDockTabsState<TId>;
  labelOf: (id: TId) => string;
  /** 单击标签 ⇒ 选中该标签（调用方按选中语义处理）。 */
  onSelect: (id: TId) => void;
  onClose: (id: TId) => void;
  onCloseAll: () => void;
  onCloseOthers: (id: TId) => void;
  onCloseRight: (id: TId) => void;
  onReorder: (id: TId, targetId: TId, before: boolean) => void;
  ariaLabel?: string;
};

export function SideDockTabStrip<TId extends string = string>({
  state,
  labelOf,
  onSelect,
  onClose,
  onCloseAll,
  onCloseOthers,
  onCloseRight,
  onReorder,
  ariaLabel,
}: SideDockTabStripProps<TId>) {
  const t = useT();
  const [menu, setMenu] = useState<{ point: ContextMenuPoint; id: TId } | null>(null);
  const dragIdRef = useRef<TId | null>(null);
  const [dropTarget, setDropTarget] = useState<{ id: TId; before: boolean } | null>(null);
  if (state.tabs.length === 0) return null;

  const handleDragStart = (event: ReactDragEvent<HTMLDivElement>, id: TId) => {
    dragIdRef.current = id;
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", String(id));
  };

  const handleDragOver = (event: ReactDragEvent<HTMLDivElement>, id: TId) => {
    if (dragIdRef.current === null || dragIdRef.current === id) return;
    event.preventDefault();
    const rect = event.currentTarget.getBoundingClientRect();
    const before = event.clientX < rect.left + rect.width / 2;
    setDropTarget({ id, before });
  };

  const handleDrop = (event: ReactDragEvent<HTMLDivElement>, id: TId) => {
    event.preventDefault();
    const from = dragIdRef.current;
    dragIdRef.current = null;
    setDropTarget(null);
    if (from === null || from === id) return;
    const rect = event.currentTarget.getBoundingClientRect();
    const before = event.clientX < rect.left + rect.width / 2;
    onReorder(from, id, before);
  };

  const menuItems = ((): ContextMenuItem[] => {
    if (!menu) return [];
    return [
      { key: "close", label: t("workspace.tabClose"), onSelect: () => onClose(menu.id) },
      { key: "close-others", label: t("workspace.tabCloseOthers"), onSelect: () => onCloseOthers(menu.id) },
      { key: "close-right", label: t("workspace.tabCloseRight"), onSelect: () => onCloseRight(menu.id) },
      { key: "close-all", label: t("workspace.tabCloseAll"), onSelect: onCloseAll },
    ];
  })();

  return (
    <div className="side-dock-tabstrip" role="tablist" aria-label={ariaLabel ?? t("workspace.previewTabs")}>
      {state.tabs.map((tab) => (
        <div
          key={tab.id}
          role="tab"
          aria-selected={state.activeId === tab.id}
          tabIndex={state.activeId === tab.id ? 0 : -1}
          draggable
          data-side-dock-tab={tab.id}
          data-side-dock-tab-kind={tab.kind}
          className={[
            "side-dock-tab",
            state.activeId === tab.id ? "side-dock-tab--active" : "",
            tab.kind === "preview" ? "side-dock-tab--preview" : "",
            dropTarget && dropTarget.id === tab.id
              ? dropTarget.before
                ? "side-dock-tab--drop-before"
                : "side-dock-tab--drop-after"
              : "",
          ].filter(Boolean).join(" ")}
          onClick={() => onSelect(tab.id)}
          onDoubleClick={(event) => {
            // 双击语义由调用方（onSelect 的上层）处理；标签条自身只负责不吞事件。
            event.stopPropagation();
          }}
          onContextMenu={(event) => {
            event.preventDefault();
            event.stopPropagation();
            setMenu({ point: contextMenuPointFromEvent(event), id: tab.id });
          }}
          onDragStart={(event) => handleDragStart(event, tab.id)}
          onDragOver={(event) => handleDragOver(event, tab.id)}
          onDragLeave={() => setDropTarget((current) => (current && current.id === tab.id ? null : current))}
          onDrop={(event) => handleDrop(event, tab.id)}
          onKeyDown={(event) => {
            if (event.key === "Enter" || event.key === " ") {
              event.preventDefault();
              onSelect(tab.id);
            } else if (event.key === "Delete" || (event.ctrlKey && event.key.toLowerCase() === "w")) {
              event.preventDefault();
              onClose(tab.id);
            }
          }}
        >
          <Tooltip label={labelOf(tab.id)} wrap>
            <span className="side-dock-tab__label">{labelOf(tab.id)}</span>
          </Tooltip>
          <button
            type="button"
            className="side-dock-tab__close"
            aria-label={t("workspace.tabClose")}
            onClick={(event) => {
              event.stopPropagation();
              onClose(tab.id);
            }}
          >
            <X size={11} />
          </button>
        </div>
      ))}
      <ContextMenu
        open={menu !== null}
        point={menu?.point ?? null}
        items={menuItems}
        minWidth={150}
        ariaLabel={t("workspace.tabMenu")}
        onClose={() => setMenu(null)}
      />
    </div>
  );
}

/** 单击/双击区分（VSCode 式）：把 onClick 换成 200ms 定时器版本。
 *  踩坑：双击必须 clearTimeout，否则先闪一次预览再钉住。 */
export function useSideDockClickDiscriminator<TPayload>({
  onSingle,
  onDouble,
  delayMs = 200,
}: {
  onSingle: (payload: TPayload) => void;
  onDouble: (payload: TPayload) => void;
  delayMs?: number;
}) {
  const timerRef = useRef<number | null>(null);
  const payloadRef = useRef<TPayload | null>(null);

  const clearPending = () => {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  };

  const click = (payload: TPayload) => {
    if (timerRef.current !== null) {
      // 第二次单击落在定时器窗口内 ⇒ 双击：取消挂起的单击再钉住（不闪预览）。
      clearPending();
      onDouble(payload);
      return;
    }
    payloadRef.current = payload;
    timerRef.current = window.setTimeout(() => {
      timerRef.current = null;
      onSingle(payloadRef.current as TPayload);
    }, delayMs);
  };

  const cancel = () => {
    clearPending();
    payloadRef.current = null;
  };

  return { click, cancel };
}

/** 供测试与调用方引用的类型再导出（避免各自 import lib 细节）。 */
export type { SideDockTab, SideDockTabsState };

/** 拖拽排序工具：把 id 移到 target 之前/之后（DOM 顺序即标签顺序）。 */
export function reorderById<T extends { id: string }>(list: readonly T[], id: string, targetId: string, before: boolean): T[] {
  const from = list.findIndex((item) => item.id === id);
  if (from < 0 || id === targetId) return [...list];
  const moving = list[from];
  const without = list.filter((item) => item.id !== id);
  const to = without.findIndex((item) => item.id === targetId);
  if (to < 0) return [...without, moving];
  const insertAt = before ? to : to + 1;
  return [...without.slice(0, insertAt), moving, ...without.slice(insertAt)];
}
