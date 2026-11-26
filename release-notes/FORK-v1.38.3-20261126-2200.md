# 包117 release notes — v1.38.3-20261126-2200

> 基线：包116（v1.38.3-20261125-2259，构建点 `5efd3ebd91`）
> 构建点：`6a1c4af1ee`（main-v2-stable，含棘轮 raw 线+四钉类型修补两 commit；其后无其他提交）
> 产物：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`（74,235,526 B）
> 执行线：zcode 开发线（9 支 merge 全量本包，zcode 滚合；Reasonix 侧包117开发1 交接续作；五轮构建=一轮双门禁拦(fork-integrity 三锚换代+四钉类型修补)→二轮 pnpm install .pnpm 损坏→重装→三/四轮 E1 收敛→五轮 rc=0）
> <!-- claims: feat-tl1831,feat-tl1818,feat-tl1793r4,feat-tl1823,feat-tl1816,feat-tl1817,feat-tl1813-web,feat-tl1820-701,feat-tl1832 -->

## 一、P1 修复/机制（交接续作四件）

### tl1793r4 pre-repair 拼接读取链修复+降级态徽标（包117-A 交接收尾）
- 真缺陷=Spliced/SplicedBy 读取绑 os.Stat(owner) 成功分支内（宿主不在时已登记状态丢失=徽标蒸发）——移出条件；前端降级徽标段+bridge SplicedDegraded 类型+三语；29/29+全包 3391 PASS（5 FAIL 全预存负载抖动，2 条裸基线复现实锤）
### tl1831 context-state 注入段格式降噪（P1，用户拍板方案 B）
- 注入段中文短句化（去英文高密度形态防 provider 秒拒——tl1742 十案例实证秒拒 100% 由该段形态触发）；消费方 4 处全按标签识别零改动；**门禁锚同步**（check-fork-integrity #9520 钉旧英文串，同机制全量扫描发现，任务 1002 先例改锚）
### tl1823 app-memory 门语义 a+c 混合实施
- settled ≥3 众数 ≥2+same-context 双锚+completeShard 强制校验；含 baseline 收口 spawn pnpm 补 shell:true；**正式长跑实发现** baseline 语境可复现性问题（高/低地板差 2.2k=detached 保留池非泄漏，方向尾部更干净）——**候裁两选项随身申报**（①采集层预 soak ②保留池单立），门按定稿正确判红不掩盖
### tl1818 出包门禁 7 红全闭环
- 94 定锚裁定（前端双侧互证）+6 锚裁定无一代码真丢（5 锚过时各归因 tl1450/tl1795/tl1719/tl1741×2+1 孤儿退役引 tl1756 台账预判）；846/846 EXIT=0+自测 5/5+REASONIX.md 登记；门禁脚本逻辑零改动

## 二、新功能

### tl1817 MiMo TTS 朗读+问号间距返修
- 检测 MIMO key（tp-/sk- 前缀值形态匹配免疫槽名改写，凭据现读免重启）→朗读走 chat/completions MiMo TTS（base64 wav 回前端 key 零过桥）；401/网络自动降级回系统 TTS；实验室交互与呈现卡 mimoTts 可选档默认关；接位=播放传输层（tl1815 分段结构全保留）
- 问号按钮间距 --windows-window-controls-gap 18px+hover 对齐面板家族，菜单五项零改动
### tl1813 网页端批1+批2+#110
- 批1 滚动堆叠一行修（60 条消息可滚）+#110 会话列表 flex 压扁修复（47 项 14px→47.25px）；批2 三件：md 渲染（自包含零依赖+XSS 注入不成真）+附件上传（POST /attachments 接线）+消息图片缩略图（偏差申报：补 GET /attachment 只读接口 7 例拒收）；**真机 serve 实例+内嵌浏览器实测非源码推断**
### tl1809 子面板快捷键补齐
- 9 个面板注册默认键（Ctrl+Shift+语义字母，统一层零冲突全表三平台校验）；注册即进设置页可改绑+palette 键位徽标+幂等；钉死 97/0
### tl1815 朗读增强→见包116；tl1832 见返修/小件

## 三、返修/小件

### tl1793r3 recovery 组行反向继承（装机两轮未过的三轮返修）
- 组 key 指向缺失正本路径→显示链四站全空；两刀=组行反向继承（从任一副本 meta 反查）+子行名字位接 label；qwen27b 孤立组全显示中文名
### tl1832 会话复制子菜单补「对话名称」
- 项目栏右键+顶栏更多菜单两处各加 copy-session-name（复用 copyToClipboard 零机制改动）；四语；tl1103 三项改四项 122/0；**代补 tl1711 预存红**（键账+行数 9→10，归因注明候追认）

## 四、清理/底座

### tl1820+tl701 清理并批
- tmp 33 文件解除跟踪+根 handoff 15 文件移工作区归档+git gc 清 5 tmp_pack（509MB→315MB，garbage=0）+回收批 5 项入 _recycle 候确认+fork-release.sh 尾 count-objects 告警
- web 壳 9 文件删除（src/web 5+web.html+vite.web.config+2 壳测试；根路径 index.html 零接触；web_dist 配置保留）；tsc-test-baseline 3 对死条目同步（gate 自检拦下补——**门禁自检再立功**）
### tl1814 裸色值六组梳理（纯梳理零代码）
- 181 处全部重定位入五批次施工图+命名规范成文（35 新 token 注册）+豁免复核（扩容 97 处：预览色卡 70/重置钮 9/平台 10/崩溃屏 6/单点 2；对账更正 tl1800 崩溃屏 7 系笔误实 6）；意外收获=onboarding var/字面混用对比度潜在缺陷（B5 修复项）

## 五、装机验证清单（reasonix 开发线逐项）

1. tl1793r4：拼接登记后徽标出现（宿主缺失态也显示）；降级登记带 Degraded 徽标
2. tl1831：中英混合文本 provider 不再秒拒（tl1742 收集器连续观察归零——跨包回报）
3. tl1823：未配预算 goalMode 默认 2M 上限生效；app-memory bench 门语义 PASS（baseline 预 soak 后）
4. tl1818：出包链 fork-integrity 门全绿（846/846）
5. tl1816：实验室五处结构调整（置顶记忆归组/导航不灰/备用模型徽章/multi_edit 聚合卡/总开关退役）
6. tl1817：配 MIMO key 朗读走 MiMo 嗓音；无 key/401 降级系统 TTS；问号按钮间距
7. tl1813：网页端滚动/会话列表不压扁/md 渲染/附件上传/图片显示
8. tl1832：两处复制子菜单有「对话名称」
9. tl1820+701：仓库瘦身（gc 509→315MB）；web 壳已删无残留引用
10. tl1809/1815/1814：延续验证（快捷键/朗读分段/施工图无感知项）

## 六、候拍板/移交

- **tl1823 baseline 候裁两选项**：①采集层预 soak/地板重定义（已拍①，实施在飞）②detached 保留池单立 tasklist——本包含①实施，②若需要另立
- tl1828 新发现 2 条：compress-floor 引号迭代假绿（≈1 行修）/wails-pin 白名单缺 app-memory.yml+pr-build-gate.yml
- tl1819/1822 两步（fork-integrity/docs-links）现挂 lint 串行同受 masking——候拍板并拆（tl1828 编排修复已铺路）
- tl1830 改接线口径候确认（超 10 分钟 spinner 清除=用户可见行为变更）
- tl1793r4 事故通报：tl1817 会话曾并发进入 wt-tl1793r4（WIP 已归还 stash@{0}）——wt 占用纪律候协调线核对
- tl1806 首批清单用户确认（--confirm 真回收）；tl1808 #105 关单候升级确认
- tl1711 预存红代补候追认（tl1832 代补）；task465/tl1441 锚漂移候派
- tl1814 遗留 3 条：色卡↔定义块同步校验/红色六族族谱/onboarding 防回归
- tl1819 已滚→候 CI 下轮 run 验证 hard gate 生效
- wt 清理轮：包117 交接 wt 8 个+本批 wt 待摘（摘后验 node_modules 完整性）

## 七、判例沉淀（本轮新增）

- **notes 预写在飞支=虚报**（包111/112 事故判例固化）；**回板「已入库」前必跑 merge-base 实测**
- **门禁锚同步**：改注入段/文案形态时同机制全量扫描门禁锚（tl1831 发现 #9520 锚钉旧英文串）
- **wt 并发劫持**：两会话不可同 wt 作业（tl1817 进 wt-tl1793r4 事故）——按 wt-520 误 pop 归还先例处置
- **大文件 read_file 截断**：263KB 门禁脚本用 grep 定位+小窗口 Read
- **write_paths 子代理无 bash**：编辑+执行双轨派单或单件全包派
