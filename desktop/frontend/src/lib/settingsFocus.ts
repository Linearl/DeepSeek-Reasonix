// tl1791 — SettingsInitialFocus 类型下沉到零依赖 leaf 模块。
//
// 原因：类型原住 SettingsPanel（懒加载大 chunk）；tl1791 给联合加了 lab 变体
// 并配套 settingsFocusRequestId 读数 helper 后，App 装配层 /
// app-runtime 命令面（主 chunk）需要 import 这个 VALUE——若继续从
// SettingsPanel 取，会把整个设置面板 chunk 拖进主包（bundle 棘轮预算不可
// 接受）。类型与 helper 落 leaf（零 import），SettingsPanel 以
// `export type { … }` 转出口保持既有 import 面零改动；appNavigation/overlays
// 的 type-only import 一并改指 leaf（type import 本无运行时代价，改指只为
// 单源：类型与 helper 同住一处，不再两处各说各话）。

export type SettingsInitialFocus =
  | { target: "bot-allowlist"; connectionId?: string; requestId?: number }
  | { target: "model-access"; requestId?: number; onboarding?: boolean }
  | { target: "model-stats"; requestId: number }
  // tl1791：实验室深链落位——entry 由深链 `#settings/lab/<entryId>` 解析而来，
  // 经 App 装配层写入 navigation store 的 settingsFocus，随 initialFocus 进面板。
  // entry 为字符串（非 ExperimentFeatureId 联合）是刻意的：App 装配层不 import
  // lab 布局注册表（保持装配层轻 + 主 chunk 预算），格式由 parseLabDeepLink
  // 把关，存在性由面板消费点 findLabEntry 校验，不在布局中的 id 零行为
  // （同 764「搜索无命中」口径——落实验室页默认视图，无害）。
  | { target: "lab"; entry: string };

/** tl1791 — focus 联合扩员（lab 变体无 requestId 字段）后，判重读数统一走
 *  本 helper 按 target 判别窄化（`in` 窄化在当前 ts 配置下不收窄该联合）。
 *  语义与各调用点原 `current?.requestId ?? 0` 逐字节一致。 */
export function settingsFocusRequestId(focus: SettingsInitialFocus | null | undefined): number {
  if (focus?.target === "model-stats") return focus.requestId;
  if ((focus?.target === "bot-allowlist" || focus?.target === "model-access") && typeof focus.requestId === "number") return focus.requestId;
  return 0;
}
