# handoff-939 —— 低分屏展开侧边栏输入框下方两排空间错排修复

分支：`wt-939`（基 `main-v2-stable` dc8c677ec）；交付 commit：`b86832cb2`。

## 现象与根因

**现象**：低分辨率屏 + 展开侧边栏后，输入框（Composer）下方出现两排空白/错位空间；拖动 Composer 高度手柄后恢复正常。

**根因**：Composer 的自动高度测量 `measureTextareaAutoHeight`（`desktop/frontend/src/components/Composer.tsx`）只有三个重测触发器——

1. `text` 变化（`useLayoutEffect [text, measureTextareaAutoHeight]`）；
2. `window.resize` 事件；
3. `documentElement` 属性 MutationObserver（`data-text-size`/字体/style）。

而侧边栏展开/收起改变的是 **layout 元素**上的 `--sidebar-expanded-width` CSS 变量（`App.tsx:3364` 写入，`styles.css:2187` 的 grid-template-columns 消费），三个触发器都捕获不到。于是：输入区变窄 → 文本换行增多 → 真实 `scrollHeight` 变大，但 textarea 的 inline height 停留在旧测量值（偏低）→ 在 `composer-card--resized` 固定高度卡片（`height: calc(var(--composer-height) + var(--composer-run-strip-reserved))`）里，输入区与底部 meta 行之间出现错排空白。低分屏聊天列更窄，最先踩到换行数变化的阈值，故先在低分屏复现。

**「拖动后恢复」的解释**：拖高手柄走 `setComposerHeight` → `measureTextareaAutoHeight` 的 useCallback 依赖（`composerHeight`）变化 → 上述 resize effect 依赖数组 `[composerHeight, measureTextareaAutoHeight]` 重跑 → 重新测量 → 恢复。这正是「展开路径缺失失效/重算触发点」的确证。

## 修复（最小修）

`Composer.tsx` 的 resize/MutationObserver effect 内，新增对 composer 卡片自身的 `ResizeObserver`：

- 仅当**宽度**变化时调用既有的 rAF 防抖 `update()` 重测；
- 高度变化（多为测量自己写回的结果）忽略，避免「测量→写高度→再测量」回环；
- 随原 effect 的 cleanup 一起 disconnect。

改动 20 行，纯触发器补充，不改任何布局计算函数与 CSS。

## 测试

新增 `desktop/frontend/src/__tests__/composer-width-remeasure.test.tsx`（已挂入 `package.json` 的 `test:composer` 链）：

- AC1 宽度变化 → 重测并应用新 scrollHeight（含「侧边栏展开变窄换行增多」场景 150px）；
- AC2 同宽回调不重测（防回环）；
- AC3 宽度恢复 → 按当前内容重测，不残留旧值。

jsdom 无真实像素布局，用可触发的 ResizeObserver 假件 + 按元素 mock `getBoundingClientRect`/`scrollHeight` 断言触发器接线；rAF 用泵两帧 flush（`setTimeout(0)` 等不到 jsdom 的 ~16ms rAF）。

**未修对照已验**：git checkout 还原 Composer 后跑新测 = 5 FAIL（红）；恢复修复后 6 PASS（绿）。

## 验收证据

| 项 | 结果 |
|---|---|
| `tsc --noEmit` 全量 | 绿 |
| 新测 composer-width-remeasure | 6/6 |
| composer-content-sizing | 12/12 |
| composer-ime | 15/15 |
| composer-ime-probe | 3/3 |
| composer-focus-heal | 12/12 |
| composer-autosize-scrollbar-css | 12/12 |
| composer-resized-input-css | 7/7 |
| composer-quick-command-bang | 17/17 |
| composer-content-menu-running | 24/24 |
| composer-emoji-picker | 498/498 |
| rich-composer-selection | 102/102 |
| composer-run-strip | 82 PASS / **4 FAIL（预存红，基线对照同红，非本支引入）** |
| check-comment-leak | 0 leak |
| check-single-scroll-writer | OK |
| eslint（改动两文件） | 0 告警 |

预存红明细（基线 dc8c677ec 复测同名单）：`the visible Yolo option selects Yolo approval` / `runtime transition disables Ask/Auto/Yolo changes` / `approval bar stays usable while its own prompt disables the composer` / `queued guidance renders in the shelf`。

## 人工验证步骤（实机，jsdom 无法覆盖真实像素）

1. 低分辨率屏（如 1366×768）打开桌面端，侧边栏收起，Composer 输入多行文本（使其在侧边栏展开宽度下换行数 ≥2 行变化，如 3~5 行中文/英文混合）。
2. 点击展开侧边栏 → **预期**：输入框随内容增高/回流，下方无两排空白、无错位。
3. 收起侧边栏 → **预期**：输入区回落到单行/少行高度，无残留空白。
4. 拖动 Composer 高度手柄再松开 → 布局应保持正常（无「只有拖了才正常」的依赖）。
5. 对照旧版本（未修复包）同操作可复现错排。

## 残留

- 实机低分屏人工验证待用户/出包后执行（上述步骤）。
- 预存红 4 项（run-strip）非本支范围，未动。
- 兄弟路径未改：`AppRuntimeView`/`useAppSessionComposition` 树里的 `useFooterHeightLifecycle` 测的是 footer 整体高度（含 ResizeObserver），不受此缺口影响；如后续发现 hero（creation）模式同类现象，hero 用同一 `measureTextareaAutoHeight`，本修已覆盖。
