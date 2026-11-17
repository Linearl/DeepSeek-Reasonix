// tl1663：设置页只读路径行「打开位置」按钮的共用钩子（决策模式文件位置、
// 数据位置四行、hooks 设置文件行统一走这里）。
//
// 探针复用 tl1641 的 ResolveRevealablePathForTab（trim + 绝对路径归一 +
// 存在性 stat；不套 opener 可执行门禁——reveal 是 explorer /select, 定位、
// 不执行目标文件，与产物栏 reveal 探针同一契约）：
//  - 空串 = 路径缺失/不可解析 ⇒ 按钮置灰并显示缺失提示，绝不给死按钮；
//  - 非空 = 可定位的绝对路径 ⇒ 点击走 RevealPath（Windows 下即
//    explorer /select,<path>，macOS open -R，Linux xdg-open 所在目录）。
// tabID 传 ""：设置页不属于任何会话 tab，被探的三条路径全是绝对路径
// （resolved decision-patterns 文件 / StorageSettings 回传 / hooks 配置
// 文件），不走相对解析分支。
import { useCallback, useEffect, useState } from "react";
import { app } from "./bridge";

export type RevealLocation = {
  /** 探针解析出的可定位绝对路径；"" = 缺失/不可用（按钮应置灰）。 */
  target: string;
  /** 在系统文件管理器中打开并选中该路径；缺失态下为 no-op。 */
  reveal: () => Promise<void>;
};

/**
 * tl1673：文件本身不存在时的「父目录回落」。
 *
 * 场景：决策模式的 decision-patterns.md 是**注入用聚合文件**——首次决策固化
 * 轮才创建，此前永远不存在。此时「打开位置」若只有「文件不存在，无法定位」
 * 一句，就是死提示（无出路）。
 *
 * 回落规则：文件探针为空时，再探它的**所在目录**——目录存在就能定位到
 * 「文件将来会出现在哪」，比死提示有出路；目录也不存在时维持原样（置灰）。
 * 只回落一级（不递归向上找），避免「随便落到某个存在的祖先目录」造成
 * 指向误导。
 */
export type RevealLocationWithFallback = RevealLocation & {
  /** 父目录回落目标（可定位的目录绝对路径）；"" = 无回落可用。 */
  fallbackTarget: string;
  /** 打开父目录；无回落时为 no-op。 */
  revealFallback: () => Promise<void>;
  /** 文件缺失但父目录可用——调用方据此把按钮降级为「打开所在文件夹」。 */
  fileMissing: boolean;
};

function parentDirOf(path: string): string {
  const trimmed = path.trim().replace(/[\\/]+$/, "");
  const idx = Math.max(trimmed.lastIndexOf("\\"), trimmed.lastIndexOf("/"));
  if (idx <= 0) return "";
  return trimmed.slice(0, idx);
}

export function useRevealLocation(value: string): RevealLocation {
  const [target, setTarget] = useState("");
  useEffect(() => {
    let alive = true;
    const probe = value.trim();
    if (probe === "") {
      setTarget("");
      return;
    }
    // 同步抛错（部分测试替身/降级环境没有该绑定）同样按「不可定位」fail-
    // closed——否则 throw 落进 React effect 会炸整棵组件树。
    try {
      void app.ResolveRevealablePathForTab("", probe)
        .then((resolved) => {
          if (alive) setTarget(resolved);
        })
        .catch(() => {
          // 探针拒绝按「不可定位」处理（与 tl1641 前端同款 fail-closed）。
          if (alive) setTarget("");
        });
    } catch {
      if (alive) setTarget("");
    }
    return () => {
      alive = false;
    };
  }, [value]);
  const reveal = useCallback(async () => {
    if (target === "") return;
    await app.RevealPath(target);
  }, [target]);
  return { target, reveal };
}

/**
 * useRevealLocation + 父目录回落（tl1673）。文件缺失且父目录存在时，
 * fallbackTarget 非空且 fileMissing=true——调用方把按钮降级为「打开所在
 * 文件夹」，让死提示有出路。
 */
export function useRevealLocationWithParentFallback(value: string): RevealLocationWithFallback {
  const base = useRevealLocation(value);
  const [fallbackTarget, setFallbackTarget] = useState("");
  useEffect(() => {
    let alive = true;
    const probe = value.trim();
    if (probe === "") {
      setFallbackTarget("");
      return;
    }
    // 文件本身可定位时不需要回落。
    if (base.target !== "") {
      setFallbackTarget("");
      return;
    }
    const parent = parentDirOf(probe);
    if (parent === "") {
      setFallbackTarget("");
      return;
    }
    try {
      void app.ResolveRevealablePathForTab("", parent)
        .then((resolved) => {
          if (alive) setFallbackTarget(resolved);
        })
        .catch(() => {
          if (alive) setFallbackTarget("");
        });
    } catch {
      if (alive) setFallbackTarget("");
    }
    return () => {
      alive = false;
    };
  }, [value, base.target]);
  const revealFallback = useCallback(async () => {
    if (fallbackTarget === "") return;
    await app.RevealPath(fallbackTarget);
  }, [fallbackTarget]);
  return {
    target: base.target,
    reveal: base.reveal,
    fallbackTarget,
    revealFallback,
    fileMissing: base.target === "" && fallbackTarget !== "",
  };
}
