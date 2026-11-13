# 附件引用格式权威定义（tl1565）

状态：权威源（SSOT）。消息体内附件引用的语法、路径范围、分类规则与消费方
清单以此文档为准；实现以 `desktop/frontend/src/lib/attachmentDisplay.ts` 的
正则族为唯一解析真相源（守卫测试强制，见 §5）。本文与实现不一致时，以实现
为准并修文档；出现第二套解析即违宪（准则：同一语义两套真相源必有一套腐化）。

非目标（non-goals）：本格式不定义附件的存储位置与落盘原语（见
`internal/control` 的 SaveImageDataURLIn 家族与群聊锚点说明
`desktop/channel_attachment_app.go`）；不定义模型侧引用 XML 块（
`internal/control/refs.go` 的 appendRefBlock，只进模型上下文，永不出现在
用户视图）；不引入第三种引用形态。

## 1. 语法：两种引用形态

消息体（collab channel Message.Body、会话转录文本）中的附件引用只有两种：

| 形态 | 语法 | 来源 |
|------|------|------|
| 裸引用 | `@<路径>` | composer 提交链的 @-token 组装（`formatAttachmentRefForSubmit`） |
| 命名引用 | `@[显示名](<路径>)` | composer 显示层的富形态（`formatAttachmentRefForDisplay`，attachment 源专用） |

词法与 Go 侧 @-token 语法（`internal/control/refs.go`）逐条对齐，前端镜像在
`lib/refToken.ts`：token = 一段非空白字符，其中**反斜杠转义的空格/制表符**
（`\ `、`\	`）留在 token 内，其余反斜杠是字面量（Windows 分隔符语义保留）。
因此**尾部反斜杠+空格会被当作转义空格吞进 token**——目录引用一律用正斜杠
结尾（`D:/proj/docs/`），不用 `D:\proj\docs\`。

解析入口（唯一）：`parseAttachmentRefsForDisplay(text)` 返回
`{ text: string; attachments: DisplayAttachment[] }`——显示文本剥掉引用 token，
附件列表按出现顺序产出。转换族同文件：
`replaceAttachmentRefsForDisplay`（引用→`[image]`/`[file:名]` 占位）、
`restoreAttachmentRefsForSubmit`（显示态还原提交态）、
`formatAttachmentRefForDisplay/ForSubmit`（组装）。

路径中的空白以反斜杠转义（`escapeRefPath`）；附件存储路径由存储原语生成、
天然无空白，故转义对附件是恒等操作——它服务于含空格的工作区路径。

## 2. 路径范围

一个 token 被承认为附件引用（`isDisplayReference`）当且仅当满足任一：

1. 以 `.reasonix/attachments/` 开头（附件存储相对形态，锚定所属工作区根）；
2. 是跨会话通道附件引用（`isChannelAttachmentRef`：盘符/UNC/开头斜杠的绝对
   路径，且归一化后包含 `/.reasonix/attachments/` 段——群聊附件落全局工作
   区，SaveChannelAttachment/SaveChannelClipboardImage 返回绝对路径进消息体）;
3. 以 `/` 结尾（目录引用 → folder 卡）；
4. 含 `/`（任何带分隔符的工作区路径 → workspace 文件卡）;
5. 带扩展名（裸文件名 → workspace 文件卡）。

不满足的 token（如 `@sc_dev` 提及、`@名字`）零消费——提及高亮走
ChannelMessageStream.renderBody 的名册匹配，与附件解析互不干扰。

安全边界（只识别不放大读取面）：

- 形状识别（上表 2）只做「像不像通道附件」；**真正的越界拒绝在 Go 读桥**：
  `ChannelAttachmentDataURL`（desktop/channel_attachment_app.go）只接受解析
  后落在全局工作区 `.reasonix/attachments` 内的路径，逃出即拒，前端退化为
  图标卡。任何前端预筛（扩展名白名单、`..` 过滤）都不再承担安全职责——
  tl1421 探针惯例：判据放读桥一处。
- 打开/复制类动作（tl1565 G3）统一先过 `ResolveOpenablePathForTab`（任务
  1060 门禁：绝对归一化+存在性+非可执行+相对路径按会话工作区解析，Global
  会话相对路径拒绝）；复制内容再过 `ReadLocalTextFile`（同门禁 + 2 MiB 上限
  + 二进制拒绝）。读桥拒绝的路径是「图标卡/诚实 toast」，不是静默丢弃。

## 3. 分类：DisplayAttachment

`displayAttachment(path, name)` 产出四维：

| 维度 | 取值 | 规则 |
|------|------|------|
| kind | `image` / `file` / `folder` | 图片扩展名白名单（png/jpg/jpeg/gif/webp/bmp/svg/tif/tiff）→ image；`/` 结尾 → folder；其余 file。扩展名判定只服务**渲染形态**（缩略图 vs 图标），不是安全边界 |
| source | `attachment` / `workspace` | 命中 §2.1 或 §2.2 → attachment（走附件读桥）；否则 workspace（走工作区读桥/探针） |
| ext | 大写扩展名 | folder 恒空串 |
| name | 显示名 | 命名引用取 `显示名`（清理转义/换行），裸引用取 basename |

渲染唯一组件：`MessageAttachments.tsx`（会话内 Message.tsx、跨会话
TranscriptCards.tsx、群聊 ChannelMessageStream.tsx 三处同用）。读桥分流
（`loadPreview`）：draft:/attachment: 片段 → 会话读桥；isChannelAttachmentRef
→ `ChannelAttachmentDataURL`；其余 → `AttachmentDataURL(ForTab)`。
交互（tl1565 G3）：图片=lightbox（ImageViewer，tl1289 多图导航）；文件卡
左键=默认应用打开、文件夹卡左键=打开目录；右键菜单=复制内容（文本文件）/
复制路径/打开所在文件夹。

## 4. 消费方清单（三处一致）

| 消费方 | 场景 | 用法 |
|--------|------|------|
| 跨会话发信（Message.tsx / Composer） | 收到/编辑带引用的转录文本 | `parseAttachmentRefsForDisplay` 拆显示态；`restoreAttachmentRefsForSubmit` 还原提交态 |
| 群聊（ChannelChatPanel / ChannelWorkspacePanel → ChannelMessageStream） | 群消息流渲染 | 同一 `parseAttachmentRefsForDisplay` 拆文本+附件；附件 → `MessageAttachments`；@提及作用在剥除后的文本上 |
| 会话内（Message.tsx → MessageAttachments；TranscriptCards.tsx） | 转录气泡附件卡 | 同上，同一组件同一形态 |

读侧桥边界（Go，`desktop/`）：

- `ChannelAttachmentDataURL` / `SaveChannelAttachment` /
  `SaveChannelClipboardImage`：全局工作区锚点（群聊的家），包含校验+@规范化
  （tl1441/tl1465/tl1421）。
- `ResolveOpenablePathForTab` / `OpenLocalPath` / `RevealPath`：任务 1060
  opener 家族（门禁在 `internal/localopen`）。
- `ReadLocalTextFile`（tl1565）：opener 门禁读侧延伸，复制内容专用。

## 5. 守卫与测试

- 守卫断言（tl1565 验收 3）：`src/__tests__/tl1565-attachment-interactions.test.tsx`
  剥注释扫描 `src/`——`attachmentRefsOf` 之类第二实现标识符零残留；附件路径
  正则字面量只允许出现在 `lib/attachmentDisplay.ts`；ChannelMessageStream
  无图片扩展白名单。新增任何附件解析前先改这里。
- 行为钉：tl1465-channel-image-render（跨会话图片内联/读桥收裸路径/重渲染
  不重发读桥）、tl1441-channel-capabilities ④段（成员侧可见图+文件卡+图标卡
  降级）、tl1565-attachment-interactions（三类卡片/lightbox 导航/交互接线/
  失败 toast）、attachment-display.test（解析单元）。
- Go 契约：desktop 包 `TestChannelAttachmentDataURL*`（读桥包含边界+@规范化）、
  `TestReadLocalTextFile*`（复制内容门禁）。
- 变更纪律：改 §1/§2/§3 任何一条 = 格式变更，需同步 attachmentDisplay.ts、
  本文、Go 侧消费桥，并跑上列全部测试；禁止在组件内手写引用解析。
