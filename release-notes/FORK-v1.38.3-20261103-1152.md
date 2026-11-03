# Reasonix Fork 桌面版 v1.38.3-20261103-1152 — Release Notes

> 基于 v1.38.3-20261102-0750（包67）以来：**三件滚合 + 文档站三连**——P0 recovery 副本风暴根治 + 会话存储卡返修 + 主题切深色模式根治 + changelog 按月拆分。覆盖安装即可。

## 本版内容

### 缺陷修复（P0）

- **recovery 副本风暴根治**（1352）：保存链路收敛——`RewriteAndSaveCompact` 单锁窗合并 Rewrite+SaveRewriteCompact，runOverflowDiet 等两处同形态 Diet 站点切换到单锁窗；fork 副本延迟转正（主文件 mtime 安静+内存与副本逐字稳定+双锁 try-lock 空闲才折叠回主路径）；logical topic fork 总量闸（≥2 强制 adopt+fail-loud）；侧栏过滤非 open/非 active 的 recovery 副本条目。竞态消除 3/3 PASS、总量闸/侧栏过滤验收全绿；A 改版后 control 包净减 7 个 FAIL。

### 缺陷修复（P2）

- **会话存储卡返修**（1337）：卡内改「左文右操作」三子项行——存储版本（v3 推荐+简述）、从 v4/v5 迁移（右=805 两按钮）、扫描&清理迁移备份（右=两扫描动作）；复用 settings-field 既有网格零新布局机制；验收守卫 13 定向测试全绿。
- **主题切深色模式根治**（1359）：initTheme 首帧读取 appearance 缓存，强制 dark/light 跨重启保持；消除主题模式切换双写并防止 baseStyle 冲蚀；浏览器 mock GetThemeExperience 初值对齐 settings.desktopTheme。

### 文档站

- **changelog 按月拆分**（1358）：单页巨表拆为按月分页四页（index/2026-11/2026-10/releases）+ 零嵌套平铺，根除 `</details>` 配对失衡同类土壤；修复 10-30 日组缺 `</details>` 致更早日期组嵌套折叠（12/12 配对核验）；顺带修首页 fork-highlights 入口与 key-features 两条既有断链。

## 门禁与质量

- 三件全部经合并线滚合+对账（234 归零）；go build root+desktop 全绿。
- 预存红申报（基线对照）：internal/control 11 项（schema-2 族）/ internal/agent 2 项 / internal/sessioncatalog 3 项——与基线同红，净新增红=0；1352 A 改版后 control 包反而净减 7 FAIL。

## 升级提醒

- 从包67（0750）覆盖安装即可；设置与数据全兼容。
