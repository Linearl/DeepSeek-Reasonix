# 包122 release notes — v1.38.3-20261128-1500

> 基线：包121（v1.38.3-20261128-1248，构建点 `0237fad233`）
> 构建点：`afdfe9507b`（main-v2-stable；九支滚合+依赖升级+对齐+修复）
> 执行线：zcode 开发线（九件全量：tl1857 CodeQL/tl-rotating 死码/tl1858 进度条/tl1855r2 返修/tl1850r2 重滚/tl1800b2 重滚+origin 三 squash 依赖升级+tl1851r2 红收口+对齐 commit）
> <!-- claims: feat-tl1857,feat-tl-rotating,feat-tl1858,feat-tl1855r2,feat-tl1850r2,feat-tl1800b2 -->

## 一、安全修复

### tl1857 CodeQL 七告警修复（用户批，Security and quality 清零）
- **#341 go/path-injection**（serve.go sessionFileForName）：加 HasPrefix 显式断言（PrefixCheck 官方 barrier，行为零变化，覆盖 delete/release/takeover 全部 name→path 面）
- **#337-340 go/unsafe-quoting ×4**（refs.go 同模式）：新 refAttrValue=ReplaceAll 引号转义（EscapeString 输出字节级 no-op 契约钉）
- **#336 js/clear-text-logging**（diff-gate.mjs）：--status 展示改走固定路径 storedSessionId，env 污点源摘除
- **#335 js/incomplete-sanitization**（测试文件）：正则断言改 includes 严格等价
- 定性=1 真缺陷防线补强+6 加固；红绿探针（env-secret 明文红→null 绿）+新 2 单测

## 二、死码清理

### tl-rotating rotating 死键+session_dag 两死文件删除（tl1853 普查实锤）
- rotateSessionDAG 等 19 符号全仓 grep 外部引用=0（tl1280 慢车道删除遗留）；**528 行死代码删**（session_dag_rotate.go 362+session_dag_unlocked.go 166）；三处清单位收窄（store 删除清单/备份白名单/sweep 形态表）；甄别排除 control 换位门同名机制

### tl1800b2 裸色值收口批三（B3 新常量）
- **45 处全量收口**（ctx 色板 17+中性洗 7+鲑红 6+编辑器黄 4+心跳绿 4+深底影 3+暗画布 2+surface-fixed 2，与普查报告零漂移）；22 新 token 注册（现 379）；**棘轮实测 167→122 字节精确锁底**；施工图两处同值铁律修正已声明；新钉 28/28

## 三、修复/返修

### tl1850r2 恢复 guardian.rewritelog 保护位（tl1850 误判回归修复）
- tl1853 普查实锤 guardian 自己是 rewritelog 第五入队源（guardian.go:458 走 Rewrite(guardian_merge) 落盘，磁盘实例逐字节同形）——tl1850 收窄漏数该源致归档零保全回归；白名单键+清单键双位补回（数据面 6→7 件）；新增红绿双钉（真实 guardian_merge 写链→转正→归档字节级保全全链验证）；conflict-preserved/splices 维持收窄

### tl1858 压缩进度条位置修复（用户体感 P2）
- compaction-only 段原插在回合内容与按钮行之间——现整段殿后到按钮行之后，三态同位锚定；混合段保持原位不回退 tl1701 时序保真；19 项新断言+transcript 族 18 钉全绿

### tl1855r2 本地服务页 ACP 行布局返修（用户包121 验收发现）
- 裸 checkbox（开关左/说明右，settings-toggle 全仓无 CSS 定义）换回实验室统一行形态（SettingsField+SettingsOptions 分段开关，与 tl1855 前成员行逐字同款）；功能元素全保留；新钉 20 根

## 四、依赖升级

- **origin 三 squash 并入**：#109 codeql-action 4.39.4 / #107 root go group 9 updates / #106 desktop sqlite 1.63（tl1856 逐 PR 审核判定可合并，本地 build/vet/定向测试背书）
- **desktop go.mod tidy 对齐**（#104 先例同型必做步）：indirect 提升+对齐（doublestar 4.10.7/klauspost 1.20.5/lark 3.13.0/uniseg 伪版本/x.net 0.63）
- **#108 npm group 挂起**：A/B 实测净增 ≥2.2 KiB 突破 bundle 预算红线（raw 3438.6 FAIL/3438.3），恢复条件=上游缩体积或用户裁决提预算，回帖已落
- tl1851r2 heartbeat 家族三套红收口（tl1142/tl1248 mock 补 ListAddressableSessions+task742 两断言按 tl1265 拍板换代）

## 五、装机验证清单（reasonix 开发线逐项）

1. **tl1848 已在包121**；本包重点=**tl1857**（Security and quality 7 告警应转 closed/fixed——CodeQL 下轮扫描验证）
2. tl-rotating/tl1800b2：无感知项（死码/token 收口）
3. **tl1858（用户体感件）**：压缩进行中进度条显示在回合尾部按钮行之后，不再插队
4. tl1855r2：本地服务页 ACP 行开关位置/形态与邻项一致
5. tl1850r2：无感知项（归档保全防线）
6. 依赖升级：无感知项（build 全绿背书）；#108 挂起回帖可见

## 六、候拍板/移交

- tl1853 三项已清（tl1850r2 本包+rotating 本包+subagent 收窄候低危批）
- **tl1826 B/C 批方向级拍板**（A 批 61 行清单+保留树 104 文件三案，报告 docs/reasonix/report/tl1826-安全清理计划-20261128.md）
- **M1-M6 挂死缓解方案拍板**（M1 治本一行级强烈建议，报告 docs/reasonix/report/desktop-全包挂死排查-20261128.md）
- skill_pkg timeout 一行+J0-J9 联调候执行
- 预存红账：ServeRecovery 已收口（064ac3084b）；desktop 全包 30m 病态变慢已归因（M1 落地即根治）；tl 回写归协调线
