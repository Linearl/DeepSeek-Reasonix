# 包116 release notes — v1.38.3-20261125-2259

> 基线：包115（v1.38.3-20261125-1322，构建点 `e2dd61b997`）
> 构建点：`5efd3ebd91`（main-v2-stable，含棘轮三线+两门禁拦补两 commit；其后 053362eeb3 docs commit 不在构建点内）
> 产物：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`（74,254,029 B）
> 执行线：zcode 开发线（6 支 merge 全量本包，zcode 滚合；二轮构建=一轮双门禁拦（class-contract+bridge-json-tags）→修→二轮 rc=0）
> <!-- claims: feat-tl1793r3,feat-tl1810-1812,feat-tl1674c,feat-tl1815,feat-tl1809,feat-tl-appmem -->

## 一、返修（装机验证打回件）

### tl1793r3 recovery 组行反向继承+子行名字位接 label（tl1793 装机后仍不通过的二轮返修）
- 根因定案（协调线源码+文件系统实锤，刷新已排除缓存）：组 key=`parentID+".jsonl"` 指向**已缺失的正本路径**→显示链四站全空→stem 兜底；子行后端已算对「qwen部署」但前端未消费 label 字段
- 两刀：①组行反向继承——正本缺失时组显示名从任一副本 meta 反查（列表+扫描两路径都接，门槛 MainExists=false）②子行名字位接 label 字段；③组头 hover 注明「正本已缺失」三语
- qwen27b 072821 孤立组组行+子行均显示「qwen部署」；29/29+前端 10/10 零回退

### tl1674c 拼接登记折叠正本降级（tl1674b 验证失败返修）
- 用户实测正本折叠态点「拼接登记」报 fail-closed 双路全堵——根因比假设更深：不在正本（可读 143 条），是 diet 改写瞬间在飞轮次在备份尾留 dangling tool call→LoadSession 补占位 normalizedDirty=true→严格快照 fail-closed 拒
- 修复=拼接层降级读侧（严格快照优先→dirty 降级→真不可读拒+Reason 点名缺什么）+Plan/Entry/预览全链 Degraded 标记（放行无声不成立）+降级读专用 memo；**LoadSessionContentSnapshot 严格语义一字未动**；现场调研员3 三备份全数翻「可拼」
- 防线边界：graft（合并回主线）保持 fail-closed（内容改写面防线不放松，另一半解堵候拍板）

## 二、新功能

### tl1815 朗读增强：中英混合分段朗读+en 缺失降级
- 混合文本按 CJK/非 CJK 切段逐段朗读（zh 段中文音/en 段英文音）——替代单 utterance 二分；并段防抖动（数字短段并相邻随中文连读/带字母段永不并/病态交替上限 200）；切段拼接恒等原文钉
- en 包缺失不再裸报错：现有语音尽力读+卡片三语装包指引（Windows 设置→时间和语言→语音）；纯中文零回退；新钉 78/78+tl1709 对表 63/63（两处有意对表注明）
### tl1809 子面板快捷键补齐
- 9 个命令面板可达主要子面板注册默认键（Ctrl/Cmd+Shift+语义字母，统一层零冲突全表三平台校验）；注册即进键盘快捷键设置页可改绑+palette 键位徽标+已开面板再按幂等；任务面板键跟 lab 门控；钉死 97/0
### tl1810-1812 三小件
- tl1810 任务面板优先级标签 hover tooltip（300ms 共享 Tooltip+三语 P0 阻断线/P1 高/P2 常规/P3 低优，空符号行结构性无触发器）
- tl1811 内置任务工具校验对齐 tl.py `_norm_prio`——fork 内零写 tl DB 入口实证（唯一访问面 ro），最小落地=21 条对拍用例表+核对脚本（实测全对上，不造第二套实现）；任务书符号映射笔误纠偏以代码为准
- tl1812 信件「消除→隐藏」四键三语+「删除→永久删除」限定（零行为改动）

## 三、CI/底座

### tl-appmem app-memory CI 三 shard FAIL 甄别修复
- 甄别=基线固有红（workflow 自建库 10-14 起 450 run 零绿），非 tl1808 遗漏（四类全在 Go 侧零交集）、非新越门
- 三层根因三笔修复：①bench 找已退役 Creation 按钮（tl1450 布局退役未同步）②基线量早了（per-fixture 预热巡回缺）③settled 尾足迹差语境未钉
- **应用零泄漏实证**：512 轮五维（DOM/JS 堆/活文档/wrapped DOM/逐导航增量）全平坦
- 余红=漂移门判据 vs 渲染器滞回的**门语义决策项候拍板**（候选 a 同语境带内比较/b CI Linux 三 shard 实测裁决/c settled 多读数取众数）；runtime-state.mjs 预存红独立甄别单申报

## 四、装机验证清单（reasonix 开发线逐项）

1. tl1793r3：qwen27b 存量孤立组组行+子行均显示「qwen部署」；组头 hover「正本已缺失」
2. tl1674c：折叠正本会话（如调研员3）点「拼接登记」→走通（预览带 Degraded 标记）；登记→确认→手动徽标全流程
3. tl1815：中英混合文本朗读（英文单词英文音）；纯英文未装包→尽力读+装包指引；纯中文零变化
4. tl1809：9 个面板快捷键逐个按（Ctrl+Shift+A/Delete/P/M/C/U/E/K/H）；设置→键盘快捷键可见可改绑；已开面板再按不重开
5. tl1810：任务面板 P0-P3 标签 hover 出定义；未标优先级不出空 tooltip
6. tl1812：信件「隐藏/永久删除」用词；「显示已隐藏→恢复」链路
7. tl1811：后台核对（对拍 21/21），无感知项

## 五、候拍板/移交

- app-memory 门语义三候选（a/b/c）；runtime-state.mjs 预存红独立甄别单（同 job 阻 ci.yml 全绿）
- tl1674c graft 另半堵（合并回主线 fail-closed 保留，解堵需拍板）
- manifest 5 字段漂移候派（tl1652 owner 面：settings_app 两处+sidecarsweep 三处）
- tl1802 两处拆解决断追认+tl1801 转 done+tl1739 本体状态（协调线联动态）
- tl1807 60m 预算追认+tl1312 观察项 25m 规程+2 条 ENV-FLAKY+W2/W6/W7 triage
- tl1797 悬停卡 400ms 归档；tl1800 剩余六组；tl1806 首批清单用户确认（--confirm 真回收）
- tl1815 detectSpeakLang 删除终局；tl1809 申报 58 预存族疑 lockfile 漂移（是否校准留用户）；task465/tl1441 锚漂移候派
