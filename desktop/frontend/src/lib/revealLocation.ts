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
