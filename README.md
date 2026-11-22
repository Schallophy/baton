# baton

把本机编码代理（OpenCode / Qoder / Qoder CN / WorkBuddy / ZCode）留下的会话，变成一份**可以直接粘贴给下一个 agent** 的交接文本。

它解决的不是「归档」，而是**逃生**：额度用完、上游报错、会话被中断，或者你只是想换个 agent——那一刻你没心情翻列表，只想一眼看出「谁卡了」，勾选要带走的东西，复制，继续干活。

## 用起来是什么样

启动后是一个本地页面，左栏是会话列表，右栏是交接台：

- **卡住的排最前**，带红色边和徽章（额度用完 / 上游报错 / 被中断），并对仍在跑的会话显示「进行中」，不会误报。
- 右边第一眼就是「为什么换人」，接着是**上次压缩摘要**、**压缩后的对话**、**未完成待办**、**请接手**。
- 每块都标了真实字数，勾哪个带哪个；对话可以按字数额度裁剪，也可以勾「带上压缩前的完整对话」。
- 「复制」直接进剪贴板，另有「另存为 .md」。
- 全程 loopback、无认证、不出本机；界面不写 cookie、不缓存。

交接结构是固定的，顺序不能换——接手方必须先知道为什么换人，再读上下文，否则会顺着刚失败的那条路继续走：

```
# 会话交接：<标题>
## 为什么换人
## 来源
## 上次压缩摘要
## 对话
## 未完成待办
## 请接手
```

没有压缩记录的会话也会保留「上次压缩摘要」这一段，写明「这个会话没有压缩过」；没有待办时写「没有未完成的待办」。少一段比写一句「没有」更容易被误解成漏内容。

## 安装

需要 Go 1.21+。SQLite 是纯 Go 实现（`modernc.org/sqlite`），**不需要 CGO**，`go build` 出来的就是单文件。

```bash
git clone https://github.com/Schallophy/baton
cd baton
go install .
```

`go install` 会把 `baton.exe` 放进 `$(go env GOPATH)\bin`（当前是 `C:\Users\Schallophy\go\bin`），这个目录已在你的 PATH 里，所以装完直接敲 `baton` 就能用，不用再 `go run .`。装完之后当前目录随便在哪都行。

只想编译不安装：

```bash
go build -o baton.exe .
```

国内网络如果拉不到模块：

```bash
# PowerShell
$env:GOPROXY = "https://goproxy.cn,direct"

# bash
GOPROXY=https://goproxy.cn,direct go install .
```

界面用 `go:embed` 打进二进制，**没有 Node 构建步骤**，改完 Go 代码重新 `go install .` 即可。

## 日常怎么用

只记两条：

```bash
baton up      # 后台起服务 + 开浏览器，回车就回到提示符
baton stop    # 收工，关掉后台服务
```

`baton up` 是把进程挂到后台，所以关掉终端窗口它还在跑；下次再敲 `baton`（不带子命令）会发现它已经在跑，直接把浏览器打开，不会再起第二个服务、第二个端口。

状态都记在 `~/.baton/server.json`（地址、进程号、退出令牌）。进程被杀掉后这个文件会失效，下次 `baton` 会自己识别出来并重新启动。

如果你更喜欢前台跑、看日志、Ctrl+C 结束：

```bash
baton serve              # 前台，默认端口从 7841 起，被占用就顺延
baton serve --restart    # 换新版本后重启
```

## 用法

```bash
baton                      # 打开界面；已在后台运行则只开浏览器
baton up                   # 后台启动（Ctrl+C 不影响）
baton status               # 看现在跑在哪个地址、哪个进程
baton stop                 # 停掉后台实例
baton serve --port 7841    # 前台启动，指定端口；被占用时自动换一个
baton serve --no-open      # 不自动开浏览器

baton list                 # 终端列表，卡住的带 !
baton list --stuck         # 只看卡住的
baton list --json          # 给脚本 / 后续 MCP 用

baton handoff --agent opencode --id ses_xxx
baton handoff --latest              # 最近一个卡住的
baton handoff --latest --out h.md   # 落盘
```

交接选项：

| 选项 | 作用 |
| --- | --- |
| `--note TEXT` | 给接手方的一句话：你在做什么、下一步想怎么走 |
| `--reason CODE` | `quota` / `upstream` / `aborted` / `switch` / `other`；不填就用检测结果 |
| `--full` | 带上压缩前的完整对话 |
| `--limit N` | 对话块字符上限，`0` = 不限（默认 40000） |
| `--no-summary` / `--no-messages` / `--no-todos` | 去掉对应块 |
| `--json` | 输出结构化结果（含 `chars`、`clipped`、`sections`） |

`list --json` 与 `handoff --json` 保留下来，是为了以后接 MCP 或 shell 脚本时不用再写一套解析。

## 数据从哪来

baton 只读，不写，不碰 agent 自己的数据库锁（SQLite 以 `mode=ro` 打开）。

| 助手 | 位置 |
| --- | --- |
| OpenCode | `~/.local/share/opencode/opencode.db`（`session_v2` / `session_message` / `todo`），直读 SQLite 为主 |
| Qoder | `~/.qoder/projects/*/*.jsonl` |
| Qoder CN | `~/.qoder-cn/projects/*/*.jsonl` |
| WorkBuddy | `~/.workbuddy/**/*.jsonl` |
| ZCode | `~/.zcode/cli/**` rollout 与 `~/.zcode/cli/db/db.sqlite` |

OpenCode 走直读而不是 `opencode session export`，因为**报错的那一刻 CLI 最不可靠**——正是它可能起不来的时候。CLI 只作兜底。

状态判定只信 agent 自己留下的记录，不做推断式报警：

- `idle` 消息的 `outcome`（`succeeded` / `failed` / `interrupted`）是 agent 对上一轮的判定；
- 但**用户又在它之后发了消息**，说明会话继续了，那个 `idle` 已是历史，不能标成卡住；
- assistant 的 `finish="error"` 或 `error.type` 是真失败（`provider.free-limit`、`provider.upstream`、`provider.transport` 等），即使崩溃时没写 `idle` 行也算；
- 没有 `finish` 且安静超过 8 分钟 → 被中断；`tool-calls` 静默超过 30 分钟 → 被中断；否则是「进行中」。

压缩摘要取最后一次 `compaction` 的 `summary`，对话从它的 `seq` 之后开始；更早的消息已由摘要覆盖，界面和交接文本都会写明覆盖了多少条。

## 开发

```bash
go test ./...        # 状态判定、交接文本结构、待办过滤都有测试
go vet ./...
gofmt -l .
```

目录：

```
main.go                    进程入口，只调 app.Run
internal/app/              CLI 子命令、后台实例（state.go）、端口顺延、开浏览器
internal/httpapi/          四个 API + `/api/shutdown` + 静态资源 + loopback + CSP
internal/sessions/         各 agent 的探测器与交接文本生成
web/                       index.html、styles/、js/、fonts/、favicon
```

界面复用 literouter 的 `tokens.css` / `base.css` / `components.css` / `markdown.js` / `format.js` / `ui.js`；`app.css` 只负责交接台布局。

后台实例只靠 `~/.baton/server.json` 协调：下一个 `baton` 先拿这份记录去问 `/api/meta`，版本对得上才复用，否则当过期记录清掉。`baton stop` 走 `/api/shutdown`，带一个只存在该文件里的令牌；网页跨站请求设不了这个头，所以页面无法把自己所在的服务器关掉。

## 边界

- 只监听 `127.0.0.1` / `::1` / `localhost`；传 `--host 0.0.0.0` 会被直接拒绝——baton 没有认证层，不该暴露到网络。
- 不注册开机自启、不改环境变量、不装服务：要用时 `baton up`，不用时 `baton stop`。状态只有一个 JSON 文件，删掉它等于重置。
- 只做只读读取，不替你发消息、不替你决定换谁。
- 不做自动轮询报警：你打开它的时候它扫一次，之后你点「重新扫描」。
- 摘要和对话正文里的 Markdown 标题会自动降级（代码块除外），避免和交接文本的段落抢层级。
