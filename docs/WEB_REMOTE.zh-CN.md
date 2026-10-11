# Web Remote 组网接入（手机/平板访问 Reasonix）

> 任务 701。目标：手机浏览器经 reasonix serve 直接使用会话列表、收发
> 消息与转录查看——基础体验而非全量投影（复杂面板如项目树/多 tab/设置
> 不投影）。token 鉴权全程生效。

## 0. 原理一图

```
手机浏览器 ──节点小宝映射(192.168.32.x:port)──► PC: reasonix serve
                                              ├─ /web/     Web 壳静态托管
                                              ├─ /sessions /submit /history  HTTP API
                                              └─ /events   SSE 事件流
全部路由在 token 鉴权之后；无 token / 错 token 一律 401。
```

## 1. PC 端：构建 Web 壳（一次性）

```bash
cd desktop/frontend
pnpm install            # 若还没装依赖
pnpm run build:web      # 产物在 desktop/frontend/dist-web/
```

`build:web` 是独立构建入口（vite.web.config.ts），不影响桌面版构建
产物。后续桌面壳更新后重跑一次即可。

## 2. PC 端：启动 serve 托管

命令行方式（推荐先这样验证）：

```bash
reasonix serve --addr 0.0.0.0:8787 --auth token --web-dist desktop/frontend/dist-web
```

- `--addr 0.0.0.0`：允许局域网/组网工具访问（默认 127.0.0.1 仅本机）；
- `--auth token`：强制 token 鉴权；`--token <值>` 可指定固定 token，
  省略则每次启动随机生成并在启动日志打印；
- `--web-dist`：Web 壳构建产物目录。

配置文件方式（持久化，等价）：`~/.reasonix/config.toml`（Windows 为
`%APPDATA%\reasonix\config.toml`）：

```toml
[serve]
auth_mode = "token"
# token = "自定义固定token"   # 省略则启动时生成并打印
web_dist = "C:/path/to/reasonix/desktop/frontend/dist-web"
```

启动成功会打印分享链接（token 已带在 `#` 后，不会进访问日志）：

```
reasonix serve — <label> on http://0.0.0.0:8787
  auth: token
  share: http://0.0.0.0:8787/#token=<token>
```

## 3. 组网：节点小宝映射

以既有 NAS 线先例（节点小宝映射段 192.168.32.x）为准：

1. PC 端节点小宝客户端登录，添加端口映射：
   - 内网地址：`127.0.0.1`（或 PC 局域网 IP），内网端口：`8787`（serve 端口）；
   - 协议：TCP；映射成功后节点小宝分配映射地址（`192.168.32.x:<映射端口>`）。
2. 记下映射地址，例：`192.168.32.100:18787`。
3. 纯局域网场景（手机与 PC 同一路由器）可跳过节点小宝，直接用 PC 的
   局域网 IP（`ipconfig` 查看），如 `http://192.168.1.20:8787`。

## 4. 手机端：访问

手机浏览器打开（token 用 `#` 段传递，不进 URL/服务端访问日志）：

```
http://192.168.32.x:<映射端口>/web/#token=<token>
```

壳加载后自动用 fragment 里的 token 换取 HttpOnly cookie 并清除地址栏
token。也可以先开 `http://192.168.32.x:<映射端口>/web/`，在页面的
token 表单里手动粘贴（serve 启动日志里看 token）。

登录后：
- 左上 ☰（窄屏）展开会话列表；点会话切换，`＋ 新会话` 新建；
- 底部输入框发消息，回车发送；运行中可点「停止」；
- 助手回复流式出现，「思考过程」可折叠展开；工具调用以行内卡片展示。

## 5. 访问路径速查

| 场景 | 手机访问地址 |
|------|--------------|
| 节点小宝组网 | `http://192.168.32.x:<映射端口>/web/#token=<token>` |
| 同一路由器局域网 | `http://<PC局域网IP>:8787/web/#token=<token>` |
| PC 本机验证 | `http://127.0.0.1:8787/web/#token=<token>` |

路径要点：**壳在 `/web/` 前缀下**；不带 `/web/` 打开的是 serve 内嵌
精简 UI（另一个更轻的既有网页界面，同样可用）。

## 6. 安全注意

- token 鉴权对 API 全程生效（无/错 token 一律 401），但 **HTTP 是明文
  传输**：公网不可信网络建议套 HTTPS 反代，或在可信组网隧道内使用；
- `?token=` 查询串方式仍兼容（老链接），但会留在浏览器历史，**推荐
  `#token=` 或表单输入**；
- 手机丢失时轮换 token：改 `--token` / config 后重启 serve 即可。

## 7. 故障排查

| 现象 | 处置 |
|------|------|
| 打开 `/web/` 提示 web shell is not built | 未构建壳：回第 1 步 `pnpm run build:web` |
| 一直停在「连接中…」 | serve 未启动或映射端口不通；PC 本机先开 `127.0.0.1:8787/web/` 验证 |
| 登录后 401 | token 错；重启 serve 看日志里的 share 链接 |
| 列表里某会话标「被占用」 | 桌面版 Reasonix 正持有该会话租约；关闭桌面端对应会话后重试 |
| 消息发不出（403 shell commands） | `!` 开头的 shell 命令不允许经 HTTP 执行（安全设计） |
