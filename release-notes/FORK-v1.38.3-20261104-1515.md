# Release Notes — FORK v1.38.3-20261104-1515（包 73）

> 基线：上一时间戳包 v1.38.3-20261104-0330（包 72）。本包 = 归档被拒全族根治 + 主题定稿 + 两件体验修。
> 主线 tip：682da0a1b。

## 缺陷修复（核心：归档「无法归档」终解）

- **归档被拒全族根治**（tl1383，P1，用户反复报「多个对话无法归档」）：
  - **meta 错代自愈**：转正链（tl1352 rename）漏改写 branch-meta 侧车致 CAS 永久死锁（会话不可写）——转正落位即改写身份+账位对齐事件日志末条；**存量已坏的：连续两次同冲突自动触发一次对账，会话自动恢复可写**
  - **unknown holder 自愈**：holder 身份缺失（pid=0 退化）的拒绝自动 reclaim 一次（OS 锁仲裁）再走 removal guard
  - **「持有者不可识别」独立哨兵**：不再回退误导的「another window」文案，FE 三语「lease 残留（无活跃 holder）」
  - **force 补扫+僵尸条目直通**：归档目标文件缺失时直通成功（tl1363 僵尸条目族防御）
  - legacy_ 双注册同查定论：三链中两链既有身份改写、唯 rename 链缺（本轮补齐），无第三处遗漏
- **审批徽章残留根修**（tl1384）：批准后侧栏「待确认」徽章不清除——结算事件两 kind（PromptAnswered/Closed）未接状态映射表，根修+四拍回归钉
- **计费文案简化**（tl1386）：去「含 N 条无定价按 0」啰嗦后缀，0 元规范显示 ¥0.00（数据层不动，tl1379 口径不回归）

## 主题定稿（用户拍板）

- **媒体风格组 8→5**（tl1385）：砍苹果高级感/无印良品/国家地理（风格依赖排版而主题系统无排版能力）；五名统一「XX风」：金融时报风/奶油色/宜家/经济学人/森系杂志风；locale 预算同步下降

## 门禁

构建 EXIT=0（二跑；首跑 E1=eslint shim 缺失系 junction 事故后遗症，pnpm rebuild 恢复）；vet=0 / 双模块 build=0 / control 包级 223.6s 全绿 / agent 包级 524.7s 全绿 / desktop 定向 22 模式 107 PASS / FE theme-pack 367 绿。
预存欠账单列：TestConcurrentLoadDuringAppendsStaysConsistent（锁竞争负载敏感，消解支处理中）/billing TestAggregateMixedCurrencies 偶发（连跑 5 绿）/queryCoalesce 跨测试缓存泄漏（候立项）/tl1240 mock 毫秒碰撞（候小件）。
