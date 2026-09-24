# FORK v1.38.3-20260924-2113

> 基线：`v1.38.3-20260924-1624`（只写本版新增；历史沿 [`FORK-v1.38.3.md`](FORK-v1.38.3.md) 链式回溯）

## 本版新增（零散三件 299/301/300，全部经独立审计 PASS）

### 1. 任务 299 —— autopilot / 子会话不再弹「中断工具需核实」
无人值守的 turn（autopilot、跨会话协作 turn）全程保持 recovery fence 豁免：**全部 8 个 orchestrator 调用点**绑定豁免上下文（含 runSubagentSkillSlash 闭包内、turn_images 三个 wrapper 自绑），配**动态守卫**（扫调用点全集 + ≥8 站点下限断言，防扫描坏了假绿）。合入 `d2426ebb2`。

### 2. 任务 301 —— composer 的 effort 档位按真实档位显示
输入框旁 effort 下拉不再显示 minimal / xhigh / max / ultra 等兼容别名，只显 **auto / None / Low / Medium / High** 五档（EFFORT_PRESETS 接入 composer；stored=xhigh 归一显示 High；wire 层 8 档兼容语义未动）。合入 `d9f01412a`。

### 3. 任务 300 —— 重启后「引导消息不再重放」
三层根因修复：① 跨进程恢复先于 Recover 的时序缺口（263 的 drop 在真实重启序下永不执行）——Recover 前移到 Snapshot/banner 之前；② 已处理（acked）的 Queued/Blocked/Uncertain 条目此前从不 drop——settled drop 扩到全 pending 态，`Recovered N` 改为重算永不累加；③ auto-resume 误把残留 pending 内容直接 dispatch 进对话——改被动恢复（SetInboxPausedPassive），真 pending 只告知不重播，留 /queue 审阅。合入 `a43ffa6b4`。

### 4. 构建与类型债收尾
- wails regen 后 `desktopProjectAdapter` required 字段兜底（tsc 0 错维持）
- 预存红台账新增 #5（`composer-run-strip` 4 红，301 审计三态归因定音非引入）

## 前一包补记：1624（2026-09-24）
1624 未写 notes，随本版补记：E1 guard v2（wails 前显式 pnpm build fail-closed，根治「wails 吞 pnpm 失败用旧 dist」）+ E1b 分支断言锁 `main-v2-stable`（防构建源错落）+ bridge AppBindings 类型债清零（47 typeof 签名补 + 60 子接口重名删，tsc 0）。

## 本版未含（第 0 步合入扫描判定）
- `develop/mimo-batch6`：8 提交内容已等价入 main（264 取我方版 `fbc0cb3cb`、E1 guard/第 0 步/AB 教训为回放等价），分支留待 MiMo 报告收尾裁决
- 历史 `feat/*`、`wt-*` 老分支：cherry 核过等价已入（wt-194/wt-M-a 全 `-`；wt-254 对应 `da38cd88c`）或为上游 rebase 工作分支（wt-273-upstream/wt-10143-rebase）
- 已知遗留（非本包范围）：wt-187 的 task 212 提交无同 hash 入档（内容疑似已随批合入，待核）；wt-254 `a2513308b` 第三层 256 补丁以 `da38cd88c` 等价在，装机侧「256 三形态」验证仍挂用户清单

## 升级提醒
从 `v1.38.3-20260924-1624` 升级：会话 / 配置 / 项目数据无迁移；回滚 = 重启并更新里切回 1624。
