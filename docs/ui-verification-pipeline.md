# UI 验证流水线（task 233 批三）

> 目标：装机验证清单的 UI 类项目由 **agent 自主执行**并产出证据（截图 + 日志行 + 读数），
> 人工只保留「体感类」最终确认。执行器 = agent 自己的工具；本流水线约定**清单格式、
> 证据落盘、执行循环与汇总口径**。

## 组件（谁干什么）

| 组件 | 职责 |
|---|---|
| 装机验证清单（`tasks/批X-装机验证清单.md`） | 每项一条：触发路径 + 期待结果 + （可选）推荐工具序列 |
| `screenshot` builtin（233 批一） | UI 证据：按 window_title 抓窗口 PNG 到工作区可写路径 |
| `ui_interact` builtin（233 批二） | 受控驱动：activate/click/type/key；click 坐标与截图同原点 |
| `scripts/ui-verify.mjs` | 流程件：`init`（清单→结果骨架）/ `log`（desktop.log 时间窗+模式比对）/ `summarize`（状态计数出结论行） |
| 验证 agent | 读清单 → 逐项调工具 → 写结果行 → 跑 summarize |

## 清单条目约定

清单表格行首列 = 项标签（`N. 标题（任务号）` 或 `X. 标题`），解析器按此识别。
推荐在每项正文里写明**推荐工具序列**，例：

- 项 210（版本弹窗）：`screenshot{window_title:"快速切换"}` → `view_image` 判定弹窗列表是否 4+1+1
- 项 190-M1/232：`ui-verify log desktop.log --since <ts> --patterns "hydrate reloaded history,cache veto"` →
  断言第二次切回 `reason=local-snapshot` 且无 `resident items empty`

## 执行循环（agent SOP）

1. `node scripts/ui-verify.mjs init <清单> <结果.md> --evidence-dir tasks/evidence-<批次>`
   —— 生成结果骨架（每项一行，状态默认 ⏳）。
2. 逐项执行：
   - UI 类：`screenshot`（证据 PNG 存证据目录）→ `view_image` 自查 → 需要交互时 `ui_interact`
     （先 activate，再 click/type/key）→ 复截图确认。
   - 日志类：`ui-verify log <logs/desktop/desktop.log> --since <操作时刻> --patterns <该项的判定串>`
     —— 断言计数与预期一致（如 232：第二次切回应为 `reason=local-snapshot` 且 `cache veto` 计数不增）。
   - 读数类：`read_file`/`bash` 取读数（timing ms、计数器），写进证据列。
3. 每项在结果文件回填一行：状态 + 证据（文件名或日志行原文）。
4. `node scripts/ui-verify.mjs summarize <结果.md>` 出结论行；❌ 项把现象写进证据列并回信管理会话。

## 状态枚举

| 状态 | 含义 |
|---|---|
| ✅ | agent 自验通过（证据已在证据列） |
| ⏳ | 待复测（工具链路不通/需特定数据） |
| 👤 | 用户测（体感类，agent 只出准备证据） |
| ❌ | 失败（现象 + 复现要点写进证据列，❌ 会让 summarize 退出码非 0） |

## 边界与安全

- `ui_interact` 受 `experimental_ui_driver` 门控（默认关）；验证会话需在配置开启后重启生效。
- 截图/注入只作用于本机桌面窗口；serve 远程会话的 UI 验证不在本流水线范围。
- 证据目录与结果文件随批次归档，不进 fork 代码库。
