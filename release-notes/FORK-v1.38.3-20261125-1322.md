# 包115 release notes — v1.38.3-20261125-1322

> 基线：包114（v1.38.3-20261124-2208，构建点 `08fcc08a4d`）
> 构建点：随棘轮 commit 后 tip（本 notes commit 后现查校准）
> 产物：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`
> 执行线：zcode 开发线（15 支 merge 全量本包，zcode 滚合）
> <!-- claims: feat-tl1800,feat-tl1799,feat-tl1796r2,feat-tl1798,feat-tl1793r2,feat-tl1773b,feat-tl1803,feat-tl1805,feat-tl1806,feat-tl1808,feat-tl1312,feat-tl1807,feat-tl1561b,feat-tl1802,feat-tl1804 -->

## 一、机制级（P1 旗舰：反复修的重复渲染族从机制上终结）

### tl1802 surfaceAdmission 归一化单一准入闸（P1，tl1801 盘点评审通过后实施）
- 五个数据写入者（live/hydrate/compaction/inbox/乐观恢复）此前各自携带世代判定、互认矩阵 9 格空白=复发结构性根因；现 reducer 七 case 顶部统一守卫纯函数，判定收敛单一入口（grep 可证）
- 行身份 SSOT 以 items 派生+WeakMap memo 承载**零形状变更**（比共识要求更强等价，主动偏差申报候追认）；八族回归钉零改动全绿；全套件对照新增红=空集
- 性能：千 turn 五族不回退，重放去重判定 **217-234× 提速**；W4/W5 矩阵空白格收口红转绿；W3 豁免（reconcileCompaction 兜底）
- 两处拆解决断候追认：W5 按「真双发」收窄（保合法连打）、W4 按「尾邻孪生」收窄（防 51× 回退，宁放过不错杀）
- 切端点 hydrate 双绘部分收口（重放面已收；页面直落面归 tl1739 返修，分界写明）

### tl1804 1795 镜像缺陷补全
- 会话内 join 向 autopilot 姿态立起（ArmAutopilotPosture↔Clear 对称，接线 join 向 1 点）——切档那一刻即生效，不再等下次重建；安全语义四条镜像（时长先写/maxRuntime 拒绝/grace 缺省/重复 arm no-op）；1795 主案九钉零回退

## 二、修复

### tl1793r2 recovery 副本继承原对话 title（tl1793 扩展返修）
- 写面（副本铸造口三处全覆盖，新副本出生即带名）+读面（版本管理显示链补两级继承源）并做；恢复后副本显示中文与回收站一致；desktop 定向 134/0
### tl1796r2 autopilot 选项恒显（装机实测打回返修）
- 一期 opt-in 键整链撤销，选项无条件渲染零键依赖；「异常关闭」调查=无异常写者（默认值显性化非被翻关）；存量手写键 config_key_warn 承接
### tl1803 audio 门控静默修复
- 照视频 r3 同款方子：门控关选音频弹三语 toast 含指引，>50MB 不再静默；门控开字节级不变；新钉 23/0
### tl1805 restartUpdate 归属对齐
- 渲染落位迁回「更新与反馈」卡（任务 992 用户拍板归位）；计数族 12 套全绿；搭车收口 task603 rail 灯 pin 漏跟
### tl1799 通讯录与侧栏对话名同源
- 根因=topic 改名不收敛已有 CustomTitle（旧名残留优先级最高）；共享取名函数 DisplayTitleFromMeta 唯一出处+改名写面全字段收敛+同机制第 4 处分叉（get_session_info 反序）一并收口
### tl1561b 大返回截断恢复路径收口
- 残余缺口=CI 形态输出被摘要整体替换后无路径无恢复（78KB 空承诺事故吻合）；修复=CI 命中即归档 pre-summary raw+摘要头带真实路径；存盘失败诚实标注（不写兑现不了的承诺）；78KB fixture 逐字节读回验证

## 三、新能力

### tl1312 goalMode 续跑预算门槛（DP-0012 定稿）
- 三轴预算全未配开 goalMode=默认上限 2,000,000 token（越线走既有 budget_spend 停因链，文案注明默认天花板+旋钮名）；显式预算行为逐字节不变；goal.go FSM 零改动；实错场景有界停掉+心跳自愈
### tl1773 命令面板「任务中心」入口隐藏（观察期至 2026-12-23）
- 代码零删除语义；到期无反馈清理代码（另立执行步）；agent 关键发现：usePaletteCommands 同 id 死代码条目到期一并甄别

## 四、治理/底座

### tl1806 孤儿 sidecar 清扫器+首批清单
- 真机只读扫 1835 件数据孤儿（171.7MB）+1853 陈旧句柄+4 空壳；六处置面形态表；缺省零写盘钉死+回收只移 _recycle 含 manifest+包内无删除调用路径；**正式 --confirm 候用户看清单**（`tmp/tl1806-first-inventory.txt`）
### tl1807 门禁固化两件
- desktop 包级超时三入口固化（Makefile/ci.yml/AGENTS.md 纪律段；45m→60m 依实测候追认）；tsc 基线刷新（1061 条→578 对立账，批次谱系+新增即红+植入假诊断验证）；产品代码零改动
### tl1808 GitHub 存量两件+main CI 红墙修复
- #104 go-udiff 已合并（偏差声明候追认）；#105 triage=已被 tl1774 覆盖（用户版本早于包113），回帖已发候升级确认
- main CI 红墙四类 5 笔修复：homemigrate !windows 存根（11-19 起红墙根因）/七测试文件 //go:build windows/agent 两文件耦合补 tag/desktop 五文件 gofmt/desktop go-udiff 对齐
### tl1758 动效参数统一→见包114；tl1800 剩余六组→候拍板
### 姊妹断言/锚漂移补同步 ×4
- tl1746/task1005/task895 计数与成员清单+task603 rail 灯 pin——「计数/结构换代必 grep 全 harness」判例固化

## 五、装机验证清单（reasonix 开发线逐项）

1. tl1802：长会话流式输出+切 tab+切端点反复操作→无重复渲染/漏渲染（历史四触发源场景）；上翻阅读不被拽
2. tl1804：会话中途切 autopilot 档→ask 按 autopilot 语义自决（不再傻等）；切回 Yolo→ask 阻塞（1795 语义不变）
3. tl1312：未配预算开 goalMode→默认 2M token 上限生效（暂停文案含「默认天花板」）；配了预算行为不变
4. tl1561b：构造大工具返回（CI 形态）→摘要头含路径→read_file 拿到完整原文
5. tl1793r2：回收站恢复副本→版本管理显示中文名；新造副本同样带名
6. tl1796r2：模式下拉恒有 autopilot（无任何开关依赖）；切 yolo 关 tab 重开仍在
7. tl1799：改会话名→通讯录与侧栏同步；topic 改名后通讯录不再是旧名
8. tl1798：悬停动画三档节奏统一；菜单悬停 300ms；改 --dur-normal 一行全局生效（演示）
9. tl1803：不支持音频的模型发录音→toast 提示
10. tl1805：实验室「更新与反馈」卡见重启更新开关（工具优化卡不再有）
11. tl1773：命令面板搜「任务」无「任务中心」条目
12. tl1800：界面颜色视觉零变化（回归确认）；scripts/check-css-color-ratchet 计数 197
13. tl1806：清单已出（tmp/tl1806-first-inventory.txt）——用户过目后 --confirm 执行回收（建议桌面端关闭后）
14. tl1807/1808：门禁与 CI 修复回归确认（无感知项）
15. tl1713 低收益门装机观察（延续）

## 六、候拍板/移交

- tl1802 两处拆解决断追认（W5 真双发/W4 尾邻孪生）+tl1801 转 done+tl1739 本体状态一并处理
- tl1807 60m 预算口径追认（维持 45m 仅改回一变量）；tl1312 观察项（control 全量建议规程 -timeout 25m）
- 2 条负载敏感 ENV-FLAKY（独跑 3/3 绿）候拍板修法；W2 repolint/W6 app-memory/W7 七用例 triage（tl1808 移交）
- tl1797 悬停卡 400ms 归档（不在三档内）；tl1800 剩余六组（mac 红绿灯/崩溃屏建议豁免）
- tl1806 首批清单用户确认（--confirm 真回收）；tl1808 #105 关单候用户升级确认
- 1793 probe 释放+文案三语化两小件（挂 1793 本体随返修带）；预存红两项（桩共享/测试面退场）P3 缓至大扫除批
- composer-bang 留观；tl1789 已 verified（候拍板清单信息滞后项）

## 七、判例沉淀（本轮新增）

- 计数/结构换代必 grep 全 harness（tl1746/task1005/task895/task603 四次实证）
- merge-tree 预演不可靠；大批出包前本地探针六线一次抬齐（包113 九轮→包114 二轮→包115 待验证）
- wailsjs 生成物过期=tsc 假净（20261106 判例第 2 次）；merge 后主线必须 generate 复验
- wails BrowserOpenURL 拒 file://；fresh wt 三件套（wailsjs/node_modules junction/.cmd 纯 ASCII）
- 行身份 SSOT 派生式承载零漂移（tl1802 工程依据：State 字段必有漂移风险）
