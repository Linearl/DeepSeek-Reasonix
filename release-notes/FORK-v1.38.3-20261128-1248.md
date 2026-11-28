# 包121 release notes — v1.38.3-20261128-1248

> 基线：包120（v1.38.3-20261128-0916，构建点 `9e327c64ae`；含 notes 校准/棘轮抬齐两治理 commit）
> 构建点：`0237fad233`（main-v2-stable；八支全量+tl1851r2 红收口+修锚/热修两治理 commit；三轮 rc=0——r1 integrity 锚拦空格漂移修锚/r2 签名门拦 wailsjs 过期 regenerate/r3 干净过）
> 执行线：zcode 开发线（reasonix 侧转派 9 件全量由 zcode 子代理执行交付）
> <!-- claims: feat-tl1846,feat-tl1800x,feat-tl1848,feat-tl1855,feat-tl1854,feat-tl1851,feat-tl1847,feat-tl1741x -->

## 一、P1 修复

### tl1848 ask 弹窗不弹根因修复（用户定性致命）
- 任务书预判「过桥通道缺失」不成立（通道全链在位有 469/428/461-P16 兜底钉）——真断点=**通道内时序自锁缺口**：活流 ask 因 seq 缺口入 gap queue→repair 回补→drain 直调 handler 时 repair promise 尚未 finally→acceptLive 误判在飞把 ask 推回队列→drain 空转→「seq 缺口+turn 安静等待+驻留前台」三条件合取（故仅个别机器复现，git diff 包113→包120 链路七文件 ask 面零改动=非回归长期潜伏）
- 修法=drain 循环复用既有 projectingReplayByTab 权威重放标记包裹（约 15 行零机制新建）；fence/generation/428/CAS 语义零改动；新钉三场景修复前红后绿+ask:background 通道不受影响

### tl1854 GC 远程接管 reasonix 侧适配 R1-R5（包120 主件）+R6 设计段
- R1 status 提前返回补 decorateTakeoverStatus（联调前提）；R2 空闲会话接管直接 204 不弹框（自持租约判定）+桌面闸门仅真持租约才弹；R3 否决窗口独立常量 10s 同源+超时改放行不中断回合+前端倒计时自动允许翻转；R4 协议文档双语（204/202/409+轮询字段+确认框非安全边界写明）；R5 对话级接管释放 S1-S6（全局 remoteDeviceReadOnly 标志退役）
- R6 设计段=Global 复用 servepool 池机制绑定专用 serve 子进程（不造新机制），未实施候批
- 红绿全实录+gate 9/9+两模块 build/vet 过；**J0-J9 联调归 zcode 主会话候执行**

## 二、用户需求

### tl1851 定时任务绑定对话显示与改绑
- 绑定会话显示「项目名-会话名」（id 降 tooltip/小字，改名跟随）；已有任务开放「更换绑定会话」（复用 tl1059 搜索交互零新组件；后端三面放开+在途运行防回滚守卫保 tl1363b 自愈）

### tl1847 停止按钮去文字纯色阶（用户改需求）
- 按钮内全部文案撤（只剩 Square 图标）；三层只靠颜色/动画区分（L1 琥珀脉冲/L2 红渐深倒计时/L3 定格）；档位说明移 hover title；aria-label 完整兜底含倒计时逐秒更新；i18n 未用键全保留防回滚

## 三、清理/减法

### tl1855 实验室清理三件
- 启动遮罩常开下架（开关键退役+关闭分支死码 grep 零残留+「启动与加载」页整页移除）；ACP 常驻监听挪本地服务页（补「ACP 已开也显示」防存量用户丢入口）；资源治理顶部两稳定项下架（实现/键保留）；tier 链 SOP 跟随（棘轮下调 74→73/70→67/27→25）；随件收口 2 处 tl1835 漏跟基点预存红

### tl1800x 裸色值收口批二
- 30 处高频状态色同值收口（B2 既有 token 6+B4 新常量 24）；13 新 token 注册进契约（现 357）；**裸色值棘轮 197→167 同 commit 锁底**；onboarding 钉 12→9

### tl1741x 实验室瘦身余项收口（tl1741 全闭环）
- false 分支读取层清零（types.ts 死镜像字段 5 处+App.tsx 内联退役键 4+过时 gate 注释 8 处换新+gofmt 对齐顺手收口）；**计划外收口** cdpDebugPort 家族钉预存红换代（9 断言 7 红旧形→退役新形 14 全绿+tsc-test-baseline 豁免退役）；全量 57 红逐一 tip 双树实证 wt 独有红=0

## 四、对比度/红收口

### tl1846 灵感手账浅色 ask 接管按钮对比度
- 唯一改值=disabled 停驻态 opacity 0.6→0.75（实测 2.45→3.14 达标，8 官方包 light 越界同批救回）；盲区②实测本就达标纯补钉；钉 325/0（含四面数学不可达诚实申报）

### tl1851r2 heartbeat 家族三套红收口（tl1851 滚合后显性化）
- tl1142/tl1248 真回归修复（tl1851 新 directory hook 调 ListAddressableSessions，两旧测试 mock 缺方法——各补一行空目录 mock）；task742 两断言预存红换代（heartbeatBackground 已被 tl1265 拍板升 recommended，测试钉旧 742 任务书口径未跟——双树对照实锤后按拍板换代）

## 五、装机验证清单（reasonix 开发线逐项）

1. **tl1848（重点）**：前台 tab autopilot 触发 ask→AskCard 正常弹出（另一台机器复现场景）
2. **tl1854+tl1851 联动**：GC 远程接管空闲会话不弹框直接 204；确认框倒计时自动允许；定时任务列表显示「项目名-会话名」、改绑生效
3. tl1845/1835/1837/1838 已在包118/120，延续验证
4. tl1855：实验室无「启动与加载」页/ACP 在本地服务页/资源治理顶部两行消失
5. tl1847：停止按钮无文字仅色阶，hover 出说明
6. tl1846：灵感手账浅色 ask 按钮三态可辨
7. tl1800x/tl1741x：无感知项（token 收口/退役清理）

## 六、候拍板/移交

- **tl1853 三项**：①tl1850r2 恢复 guardian.rewritelog 保护位（误判回归，建议必做）②rotating 死键+~370 行死代码删 ③subagent 15 键收窄（低危可缓）
- **tl1854 R6**（Global 虚拟项目绑定运行时）设计段已出候批实施；J0-J9 联调候执行
- skill_pkg manifest UserPromptSubmit 补 timeout 15000 一行（tl1845 遗留候拍板）
- 预存红账：tl1854 报 TestServeRecoveryEndpointsFailClosed（compact 相关归属待查）；desktop 全包 30m 交互挂死（tl1851 报，挂住用例每次不同）——两件候排查立项
- tl 回写归协调线（包119 七支+包120 五支+包121 八支+tl1851r2/tl1828x 结案）
