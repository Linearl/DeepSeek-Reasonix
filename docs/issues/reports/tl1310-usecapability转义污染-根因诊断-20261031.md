# tl1310 use_capability 转义污染：根因诊断（插入层归因）

- 任务：tl1310（P1 两步走：快速缓解先行 + 根治深查）
- 日期：2026-10-31
- 分支：wt-tl1310（基于 main-v2-stable @ 2aabdbfc8）
- 缓解段：commit ee1741f05（参数键名控制字符清洗，已交付）
- 本报告：深查段结论 + 证据链 + 钉死测试

## 一句话结论

**插入层不在本仓（host 端）**：SSE 重组、拼接、参数修复、序列化、传输五层逐一排除（各层 file:line + 钉死测试）；污染以 **JSON `\r` 转义序列形态**在 wire 文本上抵达——这是**生成文本的内容**而非传输损伤，插入层在上游：**模型 tokenizer（mimo-v2.6-flash）在切分点吐出 `\r` 转义**，或**推理网关分块注入**。两者在本仓可见面上无法二分，需 wire 抓包（见 §6）。

## 通俗版

模型吐出来的 JSON 文本里，下划线前面被塞了 `\r`（回车符的转义写法）。我们逐层检查了自家代码从「收到网络数据」到「执行工具」的每一道工序：没有一道工序会造出这个字符——它们要么原样搬运，要么只会删掉行尾回车。所以污染是上游（模型本身或它背后的服务网关）生成的，不是我们弄脏的；我们在收货口加了验货（清洗键名），脏货也能正常用。

## 1. 形态学定性：转义形态，非裸字节（关键证据）

现场污染调用（975 截图 / 1310 实测）**都通过了 JSON 解析**（错误发生在 catalog 匹配与 schema 校验层），而 Go `encoding/json` 对字符串内的**裸 0x0D 字节直接拒收**：

```
raw CR inside JSON string key: err=invalid character '\r' in string literal
```

实证钉死：`internal/provider/stream_scanner_test.go` → `TestStreamScannerWireCREscapeForms`（转义形态解析成功且解码出真实 \r 字节；裸 0x0D 形态解析失败）。

**推论**：污染以 `\r`（backslash + r 两个字符）转义序列形式存在于 wire 文本。转义序列只能由「生成 JSON 文本的一方」写出——即模型生成或网关重写，不存在「传输过程无中生有」的可能。

## 2. 五层逐一排除（file:line）

| 层 | 位置 | 机制 | 判定 |
|----|------|------|------|
| SSE 行重组 | `internal/provider/stream_scanner.go:24-30` | `bufio.ScanLines`：按 `\n` 切行，**只剥** CRLF 行尾的 `\r`；行内孤 `\r` 原样保留 | 只删不插；bufio 缓冲到完整行才返回，chunk 边界不可能劈开转义对 |
| 流拼接 | `internal/provider/openai/openai.go:1067`（mimo 走此路径，`config/load.go:2007` api.xiaomimimo.com/v1 → chat/completions）<br>`internal/provider/anthropic/anthropic.go:564`<br>`internal/provider/responses/output_calls.go:16` | `cur.Arguments += delta` 纯字符串追加 | 逐字节搬运，无变换 |
| 参数修复 | `internal/agent/run_loop.go:787` `repairTruncatedToolCallArgs` | 空参数补 `{}`、截断 JSON 补闭合 | 不插入任何字符 |
| 归一化 | `internal/tool/arguments.go:77` `NormalizeArguments` | 仅 `TrimSpace` 拷贝 | 不接触内容 |
| 传输 | provider 包无自定义 RoundTripper/中间件；HTTPS 代理只隧道不触体 | — | 无改写点 |

辅助扫描：`internal/provider` 非 test 代码中 `\r` 字面量唯一出现处为 `provider.go:578` `strings.TrimRight(out, " \t\r\n")`——**剥离向**。`internal/tool/builtin` 的 `\r` 处理（webfetch/bash/readback/linerange_edit）全部在工具自身 IO 结果上，不在模型响应路径。

**钉死测试**：`TestStreamScannerChunkBoundariesNeverAlterLines`——把含 `\r` 转义的 SSE 体在**每一个字节偏移**（覆盖 `\` 与 `r` 两转义字符之间）切开喂真实 scanner，行序列与单次扫描逐字节等同（不丢字、不插字）；并钉住「行内孤 `\r` 不是行终止符，ScanLines 保留、encoding/json 拒收」。

## 3. 与字段证据的吻合度

| 证据 | 转义序列形态预测 | 裸字节形态预测 | 实测 |
|------|-----------------|---------------|------|
| `tool:talk\r_to\r_session`（975 截图） | 能通过解析、catalog 报 not registered ✓ | 解析即死，到不了 catalog ✗ | 前者 |
| `expected\r_revision`（1310）schema 校验失败 | 能过解析、schema 报 required-missing ✓ | 到不了 schema ✗ | 前者 |
| 长 prompt 值内同样出现、短参数一次通过 | 生成侧随长度/切分点数增长 ✓ | 网关分块也随长度相关 △ | 两者皆容 |
| `\r` 紧贴下划线前方 | 下划线是 BPE 切分高频点（snake_case 拆词），token 边界处吐 `\r` 吻合 ✓ | 定长字节分块与下划线无必然关联 △ | 偏向前者 |

分布签名（短 id 两处 `\r`、中 id 一处、长值多处、极短零处）更贴近 **tokenizer 切分点采样出 `\r` 类 token**（中文上下文编码时 snake_case 更碎，切分点更密——与「中文上下文更易触发」的体感一致）；网关定长分块假说要求 `\r` 间距恒定，现有三例间距不等（4/5、9、不定），偏弱。但本仓无 wire 原始帧留存，两者不能最终二分。

## 4. 缓解段为何覆盖全部可达形态

代理层清洗（ee1741f05）作用在 **JSON 解析之后**：无论 wire 上是转义形态（解析成功进代理）还是裸字节形态（解析失败走 975 的 control-only / invalid-args 报错路径——后者本就报错明确），凡是能走到 schema 门禁的调用，键名必已被 `sanitizeArgumentKeys` 清洗。值内污染（任务书③）**有意不清洗**：prompt 值是模型内容，剥 `\r` 有损语义（多行文本合法含 `\r`），保持原样由目标工具自行处理。

## 5. 复现边界声明

任务书验收①「复现用例钉死（长 capability_id+下划线+中文上下文）」的**生成侧复现**需要真实 mimo 会话，本仓交付的是**链路复现**：wire 转义形态 → 重组/拼接（钉死不变形）→ 代理层（清洗生效、污染参数照常执行，见 `usecapability_argument_key_test.go` 22 项）。生成侧触发条件（何种 prompt/长度/上下文组合稳定诱发）依赖上游，本仓无法复现。

## 6. 根治候选（另案，不在本仓修复）

1. **wire 抓包二分**（判据明确）：在 `openai.go` StreamChat 加 env 开关的 body dump（现无 dump 设施），或 mitmproxy 对 `api.xiaomimimo.com` 抓包。判据：`\r` 间距恒定 → 网关定长分块；间距随 token 边界浮动 → tokenizer。
2. **上游反馈**：凭抓包证据向 mimo（xiaomimimo）侧反馈 tokenizer/网关缺陷。
3. 本仓侧不再加清洗层——缓解段已覆盖代理域全部可达形态；其他工具入口（非 use_capability）若有同类形态，另案按同机制扩展（本次未越界）。

## 7. 预存红与撞面申报

**预存红**（基线 2aabdbfc8 实测复现，非本分支引入）：
- `internal/agent` 包级全量（729.9s）：`TestListGroupRowsAndMemberTags`、`TestGuidanceSevenScenarios` 失败——「group rows = 9, want 8」，在基线 commit 临时 worktree 复跑同样失败（疑与近期 catalog 组注册数变化相关，capability-groups 域，建议另开任务核对组行数期望）。
- `TestParallelTasksCancelReturnsPartialAggregate` 全量长跑下失败、单跑通过——既有并发抖动。
- `gofmt -l internal/agent` 有 20 个预存未格式化文件（含 argument_validation.go、session.go 等；本分支未触碰）。

**撞面**：
- `internal/agent/usecapability_mcp_arguments.go`、`usecapability.go`：975/457/728/212/798/996 同文件族（容错层集中地，tasklist tl1310 指定作业面）。
- `internal/provider/stream_scanner_test.go`：追加测试（既有 929 stream_eof 测试同文件，只增不改）。
- 新增 `usecapability_argument_key_test.go`、本报告文件。

**测试读数**：
- 基线：capability 家族 271 项全绿零 SKIP；internal/capability ok；internal/config Lab 门禁 ok。
- 缓解段后：家族 293 项（271+新增 22）全绿；internal/capability ok；internal/provider 全量 ok（99.9s，含新增 2 项）。
