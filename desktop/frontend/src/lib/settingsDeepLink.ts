// tl1791 — 设置页深链（CDP 可自动化性改善的导航半边）。
//
// 形态：`#settings/lab/<entryId>`（entryId = lab-layout.yaml 卡 key，如
// runtimeObservability）。CDP `page.goto(url#settings/lab/...)` 单跳直达
// 「设置 → 实验室 → 目标卡」；hash 对人也可分享/收藏。
//
// 职责边界（减法优先）：
//   - 本模块只做 hash 字符串级解析与清理，零 DOM、零 React、零桥接依赖
//     （jsdom 单测零耦合，764 labSearch 同款纯函数风格）；
//   - 合法性校验（entryId 是否存在于当前布局）在消费端 SettingsPanel 做
//     （用与 rail 同源的 resolveLabLayout 布局查 findLabEntry）；
//   - 打开/落位：App 装配层监听 hashchange → openPage(settings, experimental)
//     + setSettingsFocus({target:"lab"})；设置页已挂载时由 SettingsPanel 自
//     己的 hashchange 监听重定焦点（两处监听、各管半边，互不越权）；
//   - 清理：设置页关闭或切离实验室页时 App 清 `#settings/` 前缀 hash
//     （history.replaceState，不产生历史条目、不触发 hashchange 回环）。
//
// non-goals（任务书）：不做通用自动化框架；不为 CDP 开 RPC；不接通用路由库
// （设置页只有这一个深链形态，hash 同步是单向粘合不是双向路由）。

/** 深链前缀。`#settings/` 前缀整体作为「设置页拥有」的标记（清理判定用），
 *  `#settings/lab/` 才是当前唯一实现的落位形态。 */
export const SETTINGS_HASH_PREFIX = "#settings/";
export const LAB_DEEP_LINK_PREFIX = "#settings/lab/";

/** 解析 `#settings/lab/<entryId>` → entryId；非该形态/非法字符 → null。
 *  entryId 限定 yaml 卡 key 的字符域（camelCase 标识符），杜绝
 *  `#settings/lab/../..` 之类的畸形值被当作 id 下传。 */
export function parseLabDeepLink(hash: string): string | null {
  if (typeof hash !== "string" || !hash.startsWith(LAB_DEEP_LINK_PREFIX)) return null;
  const id = hash.slice(LAB_DEEP_LINK_PREFIX.length);
  return /^[A-Za-z][A-Za-z0-9_]*$/.test(id) ? id : null;
}

/** 清掉设置页拥有的 hash（`#settings/` 前缀），非设置 hash 不动。
 *  用 replaceState：无滚动跳变、无新历史条目、不触发 hashchange（消费端
 *  已完成的落位不会被回环打断）。 */
export function clearSettingsHash(): void {
  if (typeof window === "undefined" || !window.location || !window.history) return;
  const { hash, pathname, search } = window.location;
  if (!hash || !hash.startsWith(SETTINGS_HASH_PREFIX)) return;
  try {
    window.history.replaceState(null, "", `${pathname}${search}`);
  } catch {
    // 某些嵌入环境 replaceState 受限（如 sandbox iframe 源不匹配）；退化为
    // 直写 hash —— 会触发一次 hashchange，但此时解析结果已是 null（无前缀
    // 或空 hash），监听方零动作。
    window.location.hash = "";
  }
}
