# FORK v1.38.3-20261117-1541（包98）

- **基线**：v1.38.3-20261117-0906（包97，增量=28 支滚合 / 26 支交付潮）
- **构建**：2026-11-17 15:41→15:52，一次通过；staging 已铺
- **主线**：tip 75ec36efda（28 支全部合入，祖先性 is-ancestor 未合=0）

## 修复（用户可感知）

### 压缩与投影（类③核心）
- **投影 sidecar 修复主体四件（tl1651r2，P1）**：sidecar 只认主路径收口（加载锚带来源 path+备份隔离防御钉）、diet 折叠后同步作废盘上 sidecar、A1 迁移双门（stored==hash+首条指纹，真改写仍 fail-closed）、**lineage key 逐段 diff 打点**（workspace/branch/model 哪段移动可归因）——档案袋与记录本失同步族的正主修复。
- **重启恢复注入与压缩竞态（tl1648x，P1）**：23 处裸 conversation.Add 统一收口进排队门（host_guidance_gate 通道 F），压缩在飞有界等待（上界 5min，超时保留 CAS 兜底）——「重启后压缩被恢复注入打断、18 条输入被丢」修复。
- **tl1644 批疑似回归定性（tl1644reg）**：真凶=tl1514 loop breaker 断言语义漂移（非 tl1644 批），测试内 kill switch 隔离，二分端点三证。

### 渲染与视图（类②）
- **重启双渲染+思考块不渲染（tl1653x）**：①session_changed 无条件全量重灌→指纹门（同 revision 跳二批）；②思考增量因 epoch 校验早退丢失→TurnEventProjector.reanchorEpoch 单点对齐——切 tab 才出思考、同内容渲染两遍修复。

### 性能与交互
- **effort 切换慢（tl1660）**：recovery-fork 会话放行 per-request override（accepted 留痕，14-30s 全量重建路径解禁）+ rebuild 接 363A 复用池（skills 8.5-13.2s 大头复用，推演降至 1-2s）+ build/swap 分界计时打点——「早先秒切」恢复。
- **模型列表自动拉取返修（tl1654r2）**：①编辑器挂载快照致列表不刷新→catalogRevision 重挂+no-op apply 幂等回读；②已删 provider 残留下拉→显示名 registry 快照全量同步+禁用/删除三段测试钉。
- **MiMo 接入方式下拉（tl1659）**：Token Plan/API 两档并列，切档即换 base_url 与 Key 预填，对标智谱先例。

### 工具与脚本
- **release 族 6 文件闭括号坏串（tl1661）**：glibc fail-open（尾括号 tag 放行）与 MSYS2 regcomp 恒拒一并消除为标准 semver 形态；两环境双跑+全仓同模式扫描（同病仅 6 处）+fail-open 实证钉。
- **协作中心入口消失（tl1579fix）**：一行级 S 档根因修复落地。

## 调研与结论（无代码）
- **agent 可及性 4.1-4.9 竞品对照（tl1662）**：九项×四竞品 36 格全覆盖，三态=照抄 1/子集 3/降级 5/不做 0；修正 tl1580 两处（pause 四家皆无剔除）。
- 附带：1626 申报的 469 挂点预存红被 tl1661 顺带治理。

## 已知预存红（申报）
- agent 11 红 = loop breaker 族（tl1514 引入，与 tl1651 名单一致，归并方向已明）；desktop 定向 6 红环境敏感。
- 新发现基点预存红候开件：check-desktop-build-contract.mjs:37（wails.json frontend:install 期望与基线不符）。
