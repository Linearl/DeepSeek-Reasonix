# PKG_UI_VERIFY — 装机后 UI 验证自动化（tl1382）

出包装机后，把 handoff「装机后验证点」表交给 CDP（Chrome DevTools Protocol）
逐项断言，替代人工逐项肉眼检查。人工清单只留无法自动化的面（真截图鼠标
行为 / 主题视觉观感 / 需人为构造坏环境的项）。

三态语义：

| 状态 | 含义 |
|---|---|
| `PASS` | CDP 断言通过（DOM / computed style / 画廊卡片在场） |
| `FAIL` | CDP 断言失败——装机验证要抓的回归（退出码 1） |
| `NEEDS_HUMAN` | 无法自动化的面，报告附操作/预期原文与人工指引 |

## 组成

| 件 | 职责 |
|---|---|
| `scripts/verify_pkg_ui.py` | 通用 runner：解析验证表 → 连 CDP → 执行 spec 断言 → 三态报告 |
| `scripts/verify_pkg_ui_specs/<包>.json` | 每包一份断言清单（数据，可代码评审） |
| handoff md | 验证点表（`\| 验证 \| 操作 \| 预期 \|`）是状态来源，runner 只读 |

分工沿用 ui-verify.mjs（task 233）的约定：清单解析归脚本、执行归驱动；
tl1382 补的是 CDP 执行腿。ui-verify.mjs 的 init/log/summarize 仍可用，
两者互不依赖。

## 前置条件（一次性）

1. **被验证的桌面端打开 CDP 调试端口**：设置 → 实验 →
   `experimental_cdp_debug_port`（重启生效）。端点随机端口仅绑 loopback，
   解析结果写入 `%APPDATA%\reasonix\logs\desktop\cdp-endpoint.txt`
   （task 342；路径可用 `REASONIX_CDP_ENDPOINT_FILE` 覆盖）。
2. **Python playwright**：`pip install playwright`（本机 desktop-automation
   技能线已具备，无需浏览器下载——连的是已有 WebView2）。

## 用法

```bash
# 1) 端点连通性检查
python scripts/verify_pkg_ui.py probe

# 2) 干跑：验证表 → 断言映射（不连 CDP）
python scripts/verify_pkg_ui.py plan --spec scripts/verify_pkg_ui_specs/pkg71-v1.38.3-20261103.json

# 3) 正式跑（handoff 路径默认取 spec.handoff，可 --handoff 覆盖）
python scripts/verify_pkg_ui.py run \
  --spec scripts/verify_pkg_ui_specs/pkg71-v1.38.3-20261103.json \
  --report /tmp/pkg71-ui-verify.md

# 退出码：0 = 无 FAIL（NEEDS_HUMAN 合法）；1 = 有 FAIL；2 = 端点/环境不可用
```

常用参数：

| 参数 | 说明 |
|---|---|
| `--endpoint host:port` | 直连指定端点（默认读端点文件） |
| `--endpoint-file <path>` | 端点文件路径覆盖 |
| `--handoff <md>` | 验证表来源覆盖（默认 spec.handoff，相对仓库根解析） |
| `--no-handoff` | 忽略验证表，只跑 spec checks |
| `--all` | 验证表未引用的 spec check 也执行 |
| `--report <md>` | 报告落盘路径（默认仅 stdout）；证据截图同目录 `verify_pkg_ui-evidence/` |

报告形态（节选，包71 实测）：

```
**结论: 7 项中 3 PASS / 0 FAIL / 4 需人工**
| 验证点 | 状态 | 证据/详情 |
| tl1343 主题 | PASS | 画廊 19 卡含「金融时报风」（…可预览应用） `192258-theme-media-broadsheet.png` |
| 侧栏 CSS | PASS | 29 行全部 nowrap+hidden+ellipsis … |
```

## 新包接入了：写一份 spec

每包 handoff §装机后验证点 表 → `scripts/verify_pkg_ui_specs/pkg<N>-<版本>.json`。
流程：复制上一包 spec → 按新验证表改 `match` 关键词与断言 → `plan` 核对映射
→ `run`。

check 四种 `kind`：

```jsonc
{
  "package": "v1.38.3-<时间戳>",
  "handoff": "../../../docs/handoff/fork开发-包<N>部署安装handoff-<日期>.md",
  "checks": [
    // ① eval：页面里跑一段 JS，返回 {pass, detail}（无导航，断当前视图）
    { "id": "sidebar-css-nowrap", "match": ["侧栏 CSS"], "kind": "eval",
      "js": "(() => { ...; return {pass: true, detail: '...'}; })()" },
    // ② actions：键盘/点击导航后断言（settings 导航在跑完会自动还原回工作台）
    { "id": "theme-media-broadsheet", "match": ["tl1343"], "kind": "actions",
      "steps": [
        {"press": "Escape"}, {"press": "Escape"}, {"wait_ms": 400},
        {"press": "Control+,"}, {"wait_ms": 1500},
        {"click_text": {"selector": ".settings-center__navitem", "text": "外观"}},
        {"wait_ms": 1200},
        {"click_text": {"selector": "button", "text": "浏览主题"}}
      ],
      "assert_js": "(() => { ... })()" },
    // ③ log：desktop.log 子串计数（可配 since "YYYY-MM-DDTHH:MM" / tail_lines / min_hits）
    { "id": "x", "match": ["tlXXXX"], "kind": "log", "patterns": ["delivery_decision"], "min_hits": 1 },
    // ④ manual：声明为人工面，why 进报告人工清单
    { "id": "y", "match": ["tlYYYY"], "kind": "manual", "why": "双屏真鼠标行为" }
  ]
}
```

约定与安全线：

- **match**：关键词对验证表「验证」列做子串匹配（如 `tl1343`）；没配上断言
  的验证行 → `NEEDS_HUMAN`（验证表是状态来源，不猜）。
- **click_text 危险动作 denylist**（runner 运行时强制）：视觉文本含
  删除/回滚/重启/强制/清空/停用/卸载/注销/重置/撕锁 的按钮一律拒绝执行。
  自动验证只点导航/面板切换类按钮；断言只读 DOM，不点删除、不点归档。
- **actions 默认截图**为证据；设置类导航结束后 runner 自动还原回工作台
  （副本 modal 先点头部关闭钮，再点 `#management-back`）。
- 断言 JS 的异常、选择器未命中、超时都算该 check `FAIL`（装机验证宁可误报
  也不漏报；`detail` 会带上异常文本供定位）。

## 已知边界

- **experimental_cdp_debug_port 默认关**——装机后未打开开关时端点文件为
  pending/不存在，runner 退出码 2 并给出指引（这正是设计：无开关的装机不
  应能被脚本驱动）。
- 需要构造坏环境的项（tl1367 残留锁、tl1371 pinned 坏文件——存量可能已被
  自愈读掉）保持人工；何时自动化依赖「能否在装机态稳定复现」，不强求。
- 主题视觉观感（配色好不好看）自动化只断「卡片在场可预览」，观感判断留给
  人工。
- spec 的 click_text 文本按中文 locale（`金融时报风`/`外观`/`存储与路径`）；
  换 locale 需同步改 spec。
