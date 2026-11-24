// tl1744：窗口右上角问号帮助菜单（参考 zcode 同位置二级菜单）。
//
// 形态 = 固定定位的问号图标按钮 + 受控 ContextMenu（全应用现成的菜单原语：
// portal、键盘导航、贴边收拢都是它自带的，本组件不新造菜单机制）。按钮常驻
// 在窗口控件（min/max/close）左侧；菜单项与各动作由调用方（App.tsx）组装——
// 那些动作要闭包 App 的状态（导航/设置页/新会话预填），组件只持有展示态。
// 挂载门控（windowsFramelessChrome）也由调用方决定，与窗口控件同条件。
//
// 键盘可达性走 button 原生语义：Enter/Space 由浏览器合成为 click，onClick
// 一条路径同时覆盖鼠标与键盘（锚点都取按钮 rect，ContextMenu 自带键盘聚焦）。

import { useCallback, useState } from "react";
import type { MouseEvent as ReactMouseEvent } from "react";
import { CircleHelp } from "lucide-react";
import { ContextMenu, type ContextMenuItem, type ContextMenuPoint } from "./ContextMenu";
import { useT } from "../lib/i18n";

export function HelpMenuButton({ items }: { items: ContextMenuItem[] }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [point, setPoint] = useState<ContextMenuPoint | null>(null);

  // 锚定按钮左下角（鼠标/键盘同一锚点，菜单自身会做贴边收拢），不用点击坐标
  // ——固定位置的入口按钮，锚在按钮上比锚在光标上视觉更稳。
  const openMenu = useCallback((event: ReactMouseEvent<HTMLButtonElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    setPoint({ left: rect.left, top: rect.bottom + 6 });
    setOpen(true);
  }, []);

  return (
    <>
      <button
        type="button"
        className={`help-menu-button${open ? " help-menu-button--open" : ""}`}
        aria-label={t("helpMenu.open")}
        title={t("helpMenu.open")}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={openMenu}
      >
        <CircleHelp size={16} aria-hidden="true" />
      </button>
      <ContextMenu open={open} point={point} items={items} minWidth={196} ariaLabel={t("helpMenu.open")} onClose={() => setOpen(false)} />
    </>
  );
}
