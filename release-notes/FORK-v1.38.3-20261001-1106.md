# Fork v1.38.3-20261001-1106（六批包：批十安全+协同基建+体验修复，17 件）

> 基线：v1.38.3-20260930-2219。本包=zcode 夜间协同批（ll-zcode-collaboration 流水线首航：逐件开发→逐件审计→批量合并）+ Reasonix 侧修复，integrity 191/191。

## 安全加固（415-418，审计-1 逐件 PASS）

- **415 critical 四件**：命令注入/SSRF/gateway XSS/gzip XSS 四条告警经对抗复核全判误报（dismiss），顺带加固 nosniff+securityHeaders+4 新测试
- **416 存储域**：真修 2 处（jobs.go job id `../../` 逃逸+parentSession 段校验），25 条分型
- **417 会话域**：sessionFileForName 统一收口（5 汇点，跨平台分隔符 containment 全覆盖）
- **418 低危+CI**：clear-text-logging×6 防御性真修+cache-poisoning 防护（setup-go cache:false）+分配上限 7 处

## 体验修复（用户痛点）

- **421 切 tab 偶发 7-15s 止血**：EffortForTab 磁盘加载加 2s 超时+上次成功值缓存兜底（治本留 148）
- **412 classic 底栏横排**（R2 真机 PASS）：App.tsx footer 三元 classic 分支补 utility-row 标记——三图标水平排布与 workbench 一致
- **428 ask 面板不弹**：C1 残留 cancelRequested+C2 激活清空双防御（judgeAskArrival 四支判定+askPanelGate 纯判定层）+双侧打点（下次复现可定案真根因）
- **424+380 冷缓存压缩**：无可折叠区时 park 终止（哨兵+再武装语义，终结无限重试刷屏）+对账日志（mode/bytes/durationMs+usage 成本求和）

## 协同基建（bus 通道+护栏）

- **432 bus enroll 修复**：render 序列化补 [serve.bus_mcp] 全 20 键+SaveTo 修正误落 CWD+回读断言（根治假成功）
- **433 fence 白名单**：无副作用工具（ask/读文件/只读 bash）中断自动判「未生效」快速续轮，不再弹人工核实面板
- **bus#1 发信放行**：talk_to_session 可寻址 zcode- 合成联系人（reasonix→zcode 方向打通）
- **M4a+bus3 zcodebridge**：reasonix→zcode 实时注入通道（app-server --stdio+裸 NDJSON+握手 fail-closed+sendText）+邮件→注入接线
- **430 ll-update 内置化**+旧名迁移表 4 条
- **425 开工新鲜度门禁**（97 仓 49s，三层判据）

## 其他

- **420 todo 更新时机**：系统 prompt TodoUpdatePolicy（签收/完成一步当时即刷）+todo_write 校验文案正例化
- **406 恢复围栏日志增强**（step1）
- 433/428 的打点与围栏改进联动：**只读类工具中断不再弹人工面板**（写类维持人工核实）
