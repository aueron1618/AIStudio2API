# 开发与贡献

AIStudio2API 使用 Go 调用 Google AI Studio 的 MakerSuite 私有协议，由 Vue 3 管理端展示账户、模型、冷却和请求状态。协议编码、流式解码、账户调度和公开适配都在 Go 进程内完成；默认 `WAA_BACKEND=spidermonkey` 由进程内 SpiderMonkey 生成官方 proof，Go HTTP 发送请求。可选 Camoufox 后端在固定指纹页面中执行 WAA 并通过 XHR 发送受保护请求；浏览器登录和浏览器 Cookie 验证也使用 Camoufox。

管理监听器、公开 API、账户池、协议运行时和内嵌 Vue 管理端位于同一进程。生成服务是可停止、可重新创建的公开 API 服务实例；原始 JSON+protobuf、WebChannel 与 WAA 格式见 [protocol.md](protocol.md)。

## 1. 环境、首次配置与启动

| 场景 | 必需组件 | 说明 |
| --- | --- | --- |
| Release 运行 | `aistudio2api` | 默认 SpiderMonkey 生成链路不准备浏览器；浏览器登录、浏览器 Cookie 验证或选择 Camoufox 后端时才按需准备，无需安装编译器、Node.js 或 Playwright |
| 源码运行 | Go 1.27+、CGO、C/C++ 编译器与 SpiderMonkey 静态库；推荐 Node.js 24.15/npm 11（也支持 22.18+、24.11+ 或 26+） | SQLite 与 WAA 都依赖原生编译，不支持 `CGO_ENABLED=0`；Node.js 用 Vite+ 构建前端 |
| Windows Chrome 导入 | Windows amd64、稳定版 Chrome | Go 程序直接读取本机 Profile 的 OAuth/DBSC 材料 |

Windows 用户解压发布包后双击 `aistudio2api.exe`。程序启动后自动打开管理页面，生成服务初始保持停止；账户登录、日志查看和生成服务启停均在该页面完成。

发布式源码运行需要更新前端或 Go 代码时，先结束当前管理进程，再按下面“源码首次启动”的命令构建并运行。Windows 日常开发可使用下面的开发服务器，前端支持热更新。

`start.bat` 仅作为 Windows launcher 保留：等待同目录现有可执行文件退出，原样传递 `setup`、`--cookie-file`、`--cdp`、`-open-ui=false` 等参数，不自动安装依赖或构建，不替代桌面窗口、托盘与更新器。

发布包中的 Windows `aistudio2api.exe`、macOS `AIStudio2API.app` 与 Linux AppImage、deb 为桌面版：

- 启动时窗口先显示启动画面，管理服务就绪后打开管理页面
- 有系统托盘时，关闭窗口后继续运行：双击托盘图标显示窗口，右键菜单可显示窗口、在浏览器中打开管理页面、开关开机自启动或退出；Linux 没有托盘宿主时关闭窗口即退出
- 再次启动时唤回已运行实例的窗口；`-open-ui=false` 不打开窗口，有托盘宿主时显示托盘图标
- 数据目录：Windows 为可执行文件所在目录，macOS 为 `~/Library/Application Support/AIStudio2API/`，Linux 为 `~/.config/AIStudio2API/`（设置 `XDG_CONFIG_HOME` 时为其中的 `AIStudio2API/`）；`.env`、`auth/` 与 `runtime/` 都在数据目录中
- Windows 桌面版以 `cmd /c start /wait aistudio2api.exe setup` 的形式运行命令时在该控制台中输入输出，命令行等程序结束后返回
- Windows 在替换运行中的输出文件时可能留下 `aistudio2api.exe~`，它是旧进程占用目标文件期间产生的构建副本

源码首次启动前，先按 README 配置当前平台的原生编译环境，包括 `CGO_ENABLED=1`、`CC`、`CXX`、`CGO_CXXFLAGS` 与 `CGO_LDFLAGS`，再在同一终端执行：

```powershell
cd web
npm ci
npm run build
cd ..
go run ./cmd/aistudio2api
```

### Windows 开发服务器

以下命令在项目根目录执行，使用 Windows PowerShell 5.1+、Go 1.27+ 和 README 所述的 llvm-mingw / SpiderMonkey 原生环境，推荐 Node.js 24.15 与 npm 11。命令行开发版不需要管理员权限、Wails CLI 或 WebView2。使用 `npm.cmd` 避免 `npm.ps1` 的执行策略限制，不修改系统执行策略。

本节不依赖私人的 `dev.bat`、`dev.ps1` 或 `vite.local.config.ts`，这些文件不属于发行包。保留这些脚本的本地环境也必须配置 CGO 与原生库；旧脚本若强制 `CGO_ENABLED=0`，不能用于当前源码。

#### 首次启动

在已经配置原生编译环境的终端 A 中运行：

```powershell
npm.cmd --prefix web ci
if ($LASTEXITCODE -ne 0) { throw "npm ci failed" }
npm.cmd --prefix web run build
if ($LASTEXITCODE -ne 0) { throw "Frontend build failed" }
New-Item -ItemType Directory -Path .\data\dev -Force | Out-Null
if (-not (Test-Path -LiteralPath .\data\dev\.env)) {
    Copy-Item -LiteralPath .\.env.example -Destination .\data\dev\.env
}
$env:CGO_ENABLED = "1"
go build -mod=readonly -trimpath -o .\data\dev\aistudio2api-dev.exe ./cmd/aistudio2api
if ($LASTEXITCODE -ne 0) { throw "Backend build failed" }
$env:LISTEN_ADDR = "127.0.0.1:12048"
$env:PREVIEW_LISTEN_ADDR = "127.0.0.1:12050"
$env:AISTUDIO_AUTH_STATES = "auth"
Push-Location .\data\dev
try { & .\aistudio2api-dev.exe -open-ui=false } finally { Pop-Location }
```

前端构建到 `internal/webui/dist/`，后端编译并在前台运行命令行版 `data/dev/aistudio2api-dev.exe`。首次编译需要准备 Go/npm 依赖与原生库；任何安装或构建失败都应停止，不运行旧二进制。离线验收须事先备齐依赖缓存并关闭模块下载，不把缓存缺失误判为源码错误。

后端输出就绪日志后，可以直接打开 `http://127.0.0.1:12048` 使用已构建的管理页面。需要修改 Vue、TypeScript 或样式时，在终端 B 中运行：

```powershell
$env:AISTUDIO_DEV_API_TARGET = "http://127.0.0.1:12048"
npm.cmd --prefix web run dev -- --host 127.0.0.1 --port 5173 --strictPort
```

前端热更新页面为 `http://127.0.0.1:5173`。两个服务均保持前台运行，不自动打开浏览器；前端命令不会另外启动后端。请始终使用 `127.0.0.1`，避免 `localhost` 的 IPv6 解析与后端 IPv4 监听不一致。

| 服务 | 默认地址 | 用途 |
| --- | --- | --- |
| Go 后端 | `http://127.0.0.1:12048` | 管理 API、公开生成 API、已构建的管理页面 |
| Vite 前端 | `http://127.0.0.1:5173` | 管理页面与 Playground 热更新，代理请求到 Go 后端 |
| HTML 预览 | `http://127.0.0.1:12050` | Playground HTML 预览的独立来源，不是管理页面 |

上述命令只监听本机回环地址，不向局域网或公网暴露。前端代理覆盖 `/api`（含 SSE 与登录 WebSocket）、`/health`、`/v1`、`/v1beta`、`/build/v1`、`/upload` 和 `/media`，保留原始 `Host`（`changeOrigin: false`）以满足管理接口的同源校验；不要为开发关闭这些安全检查。HTML 预览直接连接独立的预览端口，不经 Vite 代理。

#### 配置与数据隔离

首次启动只在不存在时从 `.env.example` 创建 `data/dev/.env`，不会覆盖已有开发配置。通过 `Push-Location` 把开发进程工作目录设为 `data/dev/`，避免读写根目录的 `.env`、`auth/`、`runtime/` 或正式版可执行文件；不要省略此步骤。

| 路径 | 内容 |
| --- | --- |
| `data/dev/.env` | 开发专用代理、API key、管理登录、WAA 等配置 |
| `data/dev/auth/` | 开发账户及认证状态，首次运行为空，需自行导入测试账户 |
| `data/dev/runtime/` | 开发日志、数据库、媒体、管理会话与按需下载的 Camoufox |
| `data/dev/aistudio2api-dev.exe` | 可重复构建的开发二进制 |

这些数据已由 `.gitignore` 中的 `/data/` 排除；不要提交或分享其中的 Cookie、密钥、日志或数据库。上述命令通过进程环境设置 `LISTEN_ADDR`、`PREVIEW_LISTEN_ADDR` 与 `AISTUDIO_AUTH_STATES`，相对账户路径在 `data/dev/` 下解析。其他进程环境变量仍优先于 `data/dev/.env`；测试文件配置前检查终端是否继承了同名变量。环境变量仅在当前终端及其子进程中生效。

默认 `AUTO_START=false`、`WAA_BACKEND=spidermonkey`，可不导入账户先开发管理界面；启动面板不需要下载 Camoufox。离线测试只使用临时 fixture、mock 与回环地址，不导入真实账户。真实生成、浏览器登录或 Cookie 验证属于单独的联网验收，需要明确授权并使用独立测试账户。

目录隔离不等于同一 Google 账户可同时运行多个 Worker：`%LOCALAPPDATA%/AIStudio2API/runtime-leases/` 中的邮箱租约仍为本机共享，不要删除或绕过锁。与正式服务并行测试时使用不同的测试账户。

#### 重启与参数

修改前端源码后 Vite 自动热更新；修改 Go 源码后，在终端 A 按 `Ctrl+C`，重新执行上述 `go build` 及后续启动命令。前端未变化时可以复用已有内嵌产物；`12048` 上的内嵌页面仍为上次构建的版本，实时前端应访问 `5173`。

依赖锁文件变化后，停止前端和后端，按锁文件运行 `npm.cmd --prefix web ci` 并完整重建。需要验收内嵌前端时，先运行 `npm.cmd --prefix web run build`，再重新编译后端。

| 设置 | 本节示例值 | 说明 |
| --- | --- | --- |
| `LISTEN_ADDR` | `127.0.0.1:12048` | 后端管理/API 地址 |
| `PREVIEW_LISTEN_ADDR` | `127.0.0.1:12050` | 独立 HTML 预览地址，端口不能与后端相同 |
| `AISTUDIO_DEV_API_TARGET` | `http://127.0.0.1:12048` | 前端代理目标，须匹配后端实际地址 |
| Vite `--port` | `5173` | 配合 `--strictPort`，占用时不自动换端口 |

端口被占用或被 Windows 保留时，同时调整终端 A 的监听地址和终端 B 的代理目标。停止时在两个终端分别按 `Ctrl+C`，关闭浏览器标签页不会结束服务。不要按进程名称批量终止其他 Go、Node 或正式服务进程，也不要在开发后端仍运行时重复构建同一个可执行文件。

#### 开发测试

在另一个 PowerShell 终端确认 HTTP 服务和代理可达，无需调用 Google 生成接口：

```powershell
Invoke-RestMethod http://127.0.0.1:12048/health
Invoke-RestMethod http://127.0.0.1:5173/health
```

管理面板已启动不代表生成服务已启动；默认 `STOPPED` 是正常状态。Go 测试需要已生成的 `internal/webui/dist/`，并继承 `CGO_ENABLED=1` 和原生编译、链接路径。以下命令均从项目根目录执行：

```powershell
go test -mod=readonly ./cmd/... ./internal/...
go vet ./...
npm.cmd --prefix web run typecheck
npm.cmd --prefix web run test
npm.cmd --prefix web run lint
npm.cmd --prefix web run format:check
npm.cmd --prefix web run build
```

仅检查相关包时可用 `go test -mod=readonly ./internal/config ./internal/api ./internal/app`；涉及浏览器、真实账户或上游联网的验收需另外在开发实例手动执行，不以 `/health` 成功代替。

| 问题 | 排查方式 |
| --- | --- |
| 找不到 Go、Node 或 npm | 安装上表要求的版本并重新打开终端，用 `go version`、`node --version`、`npm.cmd --version` 确认 PATH |
| 安装或构建失败 | 先处理首个错误；确认 `CGO_ENABLED=1`、C/C++ 编译器和 SpiderMonkey 头文件/库路径，再检查依赖缓存。需要安装依赖时使用 `npm ci`，不修改锁文件 |
| 端口不可用 | 停止已有开发实例或调整端口；用 `netsh interface ipv4 show excludedportrange protocol=tcp` 检查 Windows 保留端口，不必修改系统网络服务 |
| 前端请求 502 / `ECONNREFUSED` | 后端未就绪或 `AISTUDIO_DEV_API_TARGET` 与后端实际地址不一致 |
| 管理请求 401 / 403 | 检查开发配置的管理登录状态；通过同一个 `127.0.0.1` 页面登录，不要把请求改成跨域或启用 `changeOrigin: true` |
| 开发面板没有正式账户 | 开发数据隔离的预期行为，在开发实例自行导入测试账户 |
| 页面看不到最新前端代码 | 热更新访问 `5173`；访问 `12048` 时需重新构建前端并重新编译、启动后端 |

### 账户与普通启动

管理页面“账户”页的“添加账户”菜单提供从 Chrome 导入、粘贴 Cookie、从指纹浏览器导入、浏览器登录和远程添加，也可以重新登录、立即刷新、编辑、启停和删除账户。浏览器登录会在隔离 Camoufox 中完成并自动读取邮箱。

浏览器登录与重新登录的显示方式由服务端按请求决定：Linux 上 `DISPLAY` 与 `WAYLAND_DISPLAY` 都为空，或管理请求来自非回环地址、带 `Forwarded` 或 `X-Forwarded-For` 头时，使用管理页面登录窗口；其余情况在本机打开可见 Camoufox 窗口。管理页面为每次登录生成随机 ID，在 `POST /api/accounts` 或 `POST /api/accounts/{id}/login` 的 body 中以 `view` 带上该 ID，同时连接 `GET /api/login-view/{ID}` 的 WebSocket，两者在服务端取得同一个登录窗口。使用登录窗口时：

- 隔离 Camoufox 以无头方式启动，Linux 没有 `DISPLAY` 时使用服务启动的 Xvfb，页面保持 Firefox 默认刷新帧率；导航 AI Studio 不等待页面载入，登录窗口显示载入过程
- 登录窗口连接期间，服务端经 WebDriver BiDi `browsingContext.captureScreenshot` 以 JPEG（质量 0.8）截取最后打开且仍存在的顶层页面，间隔不短于 100 ms，与上一帧相同时不推送；每帧先发 JSON 元数据（页面 context、URL、标题、CSS 视口宽高与 devicePixelRatio），再发 JPEG 二进制。弹窗关闭后画面回到原页面
- 管理页面把鼠标移动、按下与抬起、滚轮、按键按下与抬起和粘贴文本经同一连接发回，附带画面所属页面；服务端按到达顺序合并成 `input.performActions`：指针为 mouse，滚轮为 wheel，`KeyboardEvent.key` 的命名键映射为 WebDriver 键码点，粘贴文本逐字符按下与抬起；页面已关闭的输入丢弃，输入内容不写日志
- 会话以 `unhandledPromptBehavior` 让 alert、confirm 与 prompt 保持打开，`browsingContext.userPromptOpened` 时登录窗口显示对话框，用户的选择以 `browsingContext.handleUserPrompt` 不经命令锁发送，打开对话框的输入命令随之返回
- 隐藏登录窗口或断开 WebSocket 不取消已接收的账户任务，可以用同一 `view` 重新连接；显式 `DELETE /api/accounts/tasks/{id}` 取消任务，等待 Camoufox、profile 与隔离登录目录等执行资源释放后返回 204 并移除任务

`setup` 提供以下五种命令行导入入口：

| 入口 | 命令 | 适用场景 |
| --- | --- | --- |
| 扫描本机 Chrome | `aistudio2api setup` | 交互选择可导入 Profile |
| 指定 Chrome 账户 | `aistudio2api setup --profile <PROFILE>` 或重复使用 `--email` | 批量、确定性导入 |
| 隔离登录 | `aistudio2api setup --login` | 可见 Camoufox 中手动完成 Google 登录，需要图形界面，没有图形界面时报错并指向管理页面的浏览器登录 |
| 文件导入 | `aistudio2api setup --storage-state <file>` | 导入 Playwright storage state 结构 |
| Cookie 导入 | `aistudio2api setup --cookie-file <file>`、`--cookie <Cookie 请求头>` 或 `--cdp <调试地址>`，账户邮箱按 Cookie 识别 | 导入浏览器扩展导出的 Cookie JSON、Netscape cookies.txt、一行 Cookie 请求头，或经远程调试地址读取指纹浏览器与 Chromium 中的 Cookie |

`setup` 还接受以下参数：

| 参数 | 作用 |
| --- | --- |
| `--chrome-root <DIR>` | 指定 Chrome User Data 根目录 |
| `--proxy <URL>` | 固定到账户初始化、WAA 与业务请求 |
| `--locale <LOCALE>` | 设置账户语言 |
| `--timezone <IANA_ZONE>` | 设置账户时区 |
| `--remote <origin>` | 账户上传到该远程实例，非本机地址必须使用 HTTPS |
| `--token <TOKEN>` | 远程实例的配对令牌，省略时从标准输入读取 |
| `--renew` | 在前台为本机上传到 `--remote` 实例的 Chrome 导入账户按时续签，用于重启电脑后恢复续签；不带 `--token` 与导入参数 |

`setup` 的导入模式：

- 文件导入：`--storage-state`
- Cookie 导入：`--cookie`、`--cookie-file` 或 `--cdp`，账户邮箱按 Cookie 验证，`--authuser N` 选择官网 `/u/N/` 会话。支持请求头、多行文本、扩展/storage-state JSON 和 Netscape 文件，UTF-8 上限 1 MiB；只保留 google.com 及其子域 Cookie 并检查签名所需凭据。优先使用受保护的文件，避免 Cookie 写入命令行历史
- `--cdp` 接受 `host:port`、HTTP 调试地址或浏览器级 `ws://` 地址，经 CDP `Storage.getCookies` 读取该浏览器的全部未分区 Cookie，再附加到第一个页面读取 `navigator.language` 与 Intl 时区，未给 `--locale`、`--timezone` 时作为账户的语言与时区
- 管理页面支持粘贴或读文件，并可选择验证方式。`POST /api/accounts/import/cookies` 接受 `{items:[{email?,cookies?,cdp?,auth_user,method?}],proxy?,locale?,timezone?}`，每批最多 100 项、整个 JSON 上限 8 MiB，每项 Cookie UTF-8 上限 1 MiB、最多 1000 条。`method` 在每项内，省略为 `browser`，在无头 Camoufox 中验证真实邮箱并保存轮换 Cookie 与指纹；显式 `http` 直接请求 AI Studio 页面识别真实邮箱并验证 `ListModels`，受 `INIT_TIMEOUT` 限制，不启动浏览器或执行 WAA。失败不隐式降级。上传的 `email`、身份、origins 与 OAuth 扩展不可信，只在真实验证成功后发布账户，已有账户不覆盖
- HTTP 导入任务成功只代表认证状态已提交，不同步新的 PageCatalog；后续显式启动服务或刷新时，才按所选后端及 Worker 预算获取目录。不能把 202、HTTP 认证通过或账户 `ready` 当成目录、WAA、生成或 Drive 授权全部就绪
- 登录、Chrome/Cookie 导入与账户编辑使用 `AccountEnvironmentFields` 的普通输入框，手工填写 locale/IANA 时区；新账户不预填地区，Cookie 对话框在“更多设置”中填写，当前未接入候选选择组件。`PUT /api/accounts/{id}` 的 `locale`、`timezone` 写入配置且运行时继续复用
- 管理页面的创建、重新登录、Chrome 与 Cookie 导入统一返回 `202 {task}`，不再提供旧 201 同步单对象。任务用独立于 HTTP 请求、但绑定管理生命周期的 context 顺序处理账户，`GET /api/accounts/tasks` 和 SSE `account_tasks` 给出当前列表；`items[].code` 记录解析、验证与重复失败。明显非法的 `method`、`auth_user`（须为 0 到 9）及旧顶层 `method` 在 202 前返回 400
- 显式 DELETE 取消并等待执行释放后返回 204、移除记录；停服取消但未 DELETE 的任务保留 `state=failed`，未完成项为 `state=failed,code=account_task_canceled`。排队项为 `pending`，执行项为 `running`，正常终态为 `done` 或 `failed`。账户任务仅在内存登记，不写入 `states.db`，重启不恢复；客户端断开不能代替 DELETE
- “从指纹浏览器导入”从本机进程命令行找出带 `--remote-debugging-port` 的浏览器（端口为 0 时读取用户数据目录的 `DevToolsActivePort`），逐个读取 Cookie 并识别账户，列出可导入、已导入与未登录的浏览器
- 隔离登录：`--login`
- 浏览器导入：Chrome 导入参数

`setup` 写入本机时要求 `AISTUDIO_AUTH_STATES` 指向一个账户目录。

加 `--remote` 时，Chrome 导入、隔离登录、文件导入与 Cookie 导入照常在本机完成，结果凭远程实例账户页“远程添加”生成的配对令牌逐个上传，隔离登录同时上传 Camoufox 指纹。远程账户使用远程实例的默认代理，`--proxy` 只用于本机导入与登录。来源带可续签 Chrome OAuth 材料时，上传完成后命令留在原电脑前台：立即并每 100 分钟以本机 Chrome 来源与 OAuth 材料签发新会话，经 `PUT /api/pairing/accounts/{account}/cookies` 凭上传响应的 `renewal_token` 更新远程 Cookie，失败时 5 分钟后重试；OAuth 与 token 保存在原电脑的 `runtime/remote-renewals.json`（权限 0600），`--remote <origin> --renew` 读取该 origin 的记录恢复续签。服务端不信任上传邮箱、OAuth 或 localStorage，按安全 Cookie 解析和真实 HTTP 验证后提交，只在 `pairing-renewal.json` 保存 token 摘要；不能在服务器独立使用原电脑的设备材料续签。没有可续签 Chrome 来源的导入完成后退出。

- `--proxy` 会同时固定到新账户的初始化、WAA 与业务请求，接受 HTTP、HTTPS 或 SOCKS5 URL，代理认证写在 URL 的 `user:password@` 部分；未指定时导入与登录使用 `PROXY` 中的第一个代理，新账户不设账户代理
- `--locale` 和 `--timezone` 设置账户环境；Chrome 导入未显式指定语言时读取 Profile 的首选语言

Camoufox 按以下顺序定位，只使用发行目录 `application.ini` 中 `Version` 与程序固定版本相同的 Camoufox：

1. 进程环境变量 `CAMOUFOX_PATH`，版本不符时报告实际版本
2. `runtime/camoufox/`
3. 可执行文件旁的同名目录
4. Windows 本机 Camoufox 缓存

版本不符的候选被跳过；都没有时自动下载当前平台的固定版本。下载失败时，错误给出发行包地址与 `runtime/camoufox/` 的绝对路径，手动下载解压到该目录后不再下载。Camoufox 在报告 WebDriver BiDi 端点前退出时，错误带上退出码与 Camoufox 最后 20 行输出；输出含 `cannot open shared object file` 时，错误只写明退出码与 `ldd` 查到的 Camoufox 及其依赖库缺少的全部系统库，Debian 系发行版另附安装这些库与 Xvfb 的 `apt-get satisfy` 命令。没有 `DISPLAY` 且找不到 Xvfb 时，错误同样附上该命令。

日常启动只需运行二进制或 Go 入口，再从管理页面启动生成服务：

```powershell
./aistudio2api.exe
go run ./cmd/aistudio2api --listen 127.0.0.1:2048 --open-ui
```

日常服务入口接受以下参数：

| 参数 | 作用 | 默认来源 |
| --- | --- | --- |
| `--auth <PATHS>` | 覆盖本次进程使用的账户文件、目录或逗号分隔路径 | `AISTUDIO_AUTH_STATES` |
| `--listen <HOST:PORT>` | 覆盖管理页面与 API 的监听地址 | `LISTEN_ADDR` |
| `--proxy <URL>` | 覆盖每次新建生成服务实例使用的全局代理 | `PROXY` |
| `--open-ui` | 启动后打开管理页面 | 无参数启动时为 `true` |

管理页面与 `/api` 控制面在生成服务停止时继续运行。停止生成服务会取消活动生成请求、当前账户任务并关闭 WAA Worker；再次启动会重新读取账户模型目录。生成服务启动期间到达的公开 API 请求等启动结束再处理，启动失败时与停止时一样返回 503 `service_stopped`，错误信息说明在管理页面启动服务或开启自动启动。命令行版关闭启动窗口或按 `Ctrl+C` 才会退出整个管理进程；桌面版从托盘退出，Linux 无托盘宿主时关闭窗口退出。

## 2. 目录、组件和运行依赖

```text
cmd/aistudio2api/        薄入口，只调用 internal/app 的 app.Run
internal/app/            配置、账户装配、认证续签、信号、生成服务实例、调度和服务生命周期
internal/setup/          Chrome 导入、storage-state 导入、Cookie 导入与隔离登录命令
internal/aistudio/       账户、MakerSuite、WAA、模型、工具、上传、媒体和规范事件
internal/api/            OpenAI、Responses、Anthropic、Gemini 与管理端 HTTP 路由
internal/camoufoxnative/ 原生 WebDriver BiDi、WAA 页面与隔离登录
internal/waa/            SpiderMonkey WAA：BotGuard VM、Firefox 形状宿主与 SpiderMonkey cgo 绑定
internal/proxydial/      上游代理拨号与带认证代理的本机 SOCKS5 中转
internal/requestdb/      请求用量账本与可选正文的 SQLite 存储
internal/statedb/        Responses 与 Interactions 续接节点的 SQLite 存储
internal/pricing/        用量估算费用使用的 Gemini API 单价表
internal/chromeauth/     Windows Chrome OAuth/DBSC 发现、导入和续签
internal/config/         全局配置的读取、校验和原子写回
internal/webui/          嵌入并提供 Vue 构建产物
internal/desktop/        桌面版窗口、系统托盘、启动画面与 Wails 更新器接入
web/                     Vue 3、TypeScript、Vite+ 和 Tailwind CSS 源码
docs/                    开发流程与私有协议说明
build/                   应用图标、Windows 资源、macOS Info.plist 与 Linux 打包配置
auth/                    每账户配置、认证状态和可恢复运行状态
runtime/camoufox/        Release 使用的 Camoufox 运行时
runtime/requests.db      请求用量账本与可选正文
runtime/states.db        Responses、Interactions 与 Chat 音频回复的续接节点
runtime/media/           MEDIA_OUTPUT=url 时按内容命名保存的生成媒体
```

主依赖方向：

```text
cmd/aistudio2api
  -> internal/app
       -> internal/api
       -> internal/aistudio
       -> internal/setup
       -> internal/camoufoxnative
       -> internal/config
       -> internal/webui

internal/setup
  -> internal/aistudio
  -> internal/camoufoxnative
  -> internal/chromeauth
  -> internal/config
```

请求链保持单向：

```text
HTTP route
  -> client protocol decoder
  -> canonical request
  -> capability-aware account lease
  -> AI Studio array encoder
  -> per-account WAA proof
  -> Go HTTP transport (default) / fingerprinted Camoufox XHR
  -> incremental response decoder
  -> canonical events
  -> client protocol response
```

WebSocket 入口沿用相同分层：`internal/api` 解码公开协议，`internal/app` 绑定账户和运行状态，`internal/aistudio` 执行 WebChannel 与规范事件转换。公开适配器只消费规范请求与事件；账户文件、WAA 对象、原始数组和资源粘性由 `internal/aistudio` 与 `internal/app` 管理。

Camoufox 由 Go 通过 WebDriver BiDi 直接管理。启动数据面时，服务按 `WARM_WORKER_LIMIT` 与 `WARM_STARTUP_CONCURRENCY` 准备隔离、无头、长驻的账户 runtime，并在需要其他账户能力时替换最久未用的空闲 runtime。无头 runtime 的软件 vsync 为每秒 60 次，空闲时以 `gfx.display.frame-rate-divisor` 每 60 次分发一次，页面每秒刷新 1 帧；界面运行的输入、点击与新开对话期间分频为 1，页面从下一次 vsync 起每秒刷新 60 帧，WebRender 以单通道灰度光栅化字形；官网页面就绪后，服务经 WebDriver BiDi 访问浏览器界面层（Camoufox 以 `MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1` 启动），把标签页的 `renderLayers` 设为 false，标签页不再光栅化与合成，页面的可见状态、焦点、动画帧与定时器不变。WebGL 在所有主机上强制启用，取账户指纹中的 Windows 显卡配置；语音列表在所有主机上只含账户指纹中的语音。非 Windows 主机上，Camoufox 按 Windows 版 Firefox 的字体偏好解析字体名，字体白名单补入已声明字体所在字体文件的其他字体族名，并启用 WebGPU、Windows 10 滚动条样式与 2 声道 AudioContext；`window.innerHeight` 取 `window.outerHeight` 减 Windows 浏览器界面高度 57，站点存储的全局上限固定为 50 GiB；无头时经 WebDriver BiDi 移动主窗口，使页面内容区的屏幕坐标与 Windows 版一致。Linux 上 Camoufox 只加载发行包内的 Windows 字体（字形微调为 `hintslight`），按 Windows 字体替换表解析字体名；macOS 上服务在启动 Camoufox 前把 Windows 字体替换名写入发行包字体的 name 表，并给这些字体的 PostScript 名加 `-Windows` 后缀，避免与系统同名字体冲突。没有 `DISPLAY` 的 Linux 主机上，每个无头 Camoufox 使用服务启动的 Xvfb 虚拟显示，WebGL 经 Mesa GLX 软件渲染，Camoufox 退出时一并结束该 Xvfb。每个 runtime 使用关闭时删除的临时 profile，HTTP 磁盘缓存写入账户目录的 `camoufox-cache/`，同一账户重启时复用官网静态资源；同一账户同时存在的第二个 runtime 使用临时 profile 内的缓存。创建临时 profile 的进程在 profile 内持有锁文件，运行时装配时删除锁已释放的遗留 profile。Windows 上每个 Camoufox 进程树加入服务进程持有的 Job，服务进程退出时由系统一并结束。每个 runtime 以临时对话打开官网新对话页 `/prompts/new_chat?model=<模型>&temporary=true`：页面脚本之前注入的预载脚本取得官网应用的 WAA service 对象，页面发出的 MakerSuite 请求提供动态请求头，`GetLoggingContext` 响应提供 `X-Goog-Ext-519733851-Bin`。最后一轮是带文本的 user 轮次的 GenerateContent 在页面界面运行：输入框没有焦点时服务先以可信指针点击输入框（官网新开对话后焦点已在输入框中），再在输入框输入该轮各文本 Part 以空格连接的文本并以可信指针点击 Run；一轮返回 200 且响应正文读完后，服务在后台点击 New chat 并丢弃该对话（临时对话中新开的对话仍是临时对话），下一轮在空白对话中直接输入，期间到达的请求等待该标签页；其余情况下标签页保留该对话，与上一轮的对话相同时点击该轮的 Rerun，在其后追加 model 与 user 两轮时在当前对话中继续，其余对话先点击 New chat 并丢弃当前对话；页面应用生成 proof 并创建 GenerateContent 的 `XMLHttpRequest`，发送时正文换成 Go 编码的业务正文与这份 proof，响应头与响应块由页面经 WebDriver BiDi `script.message` 通道推送给 Go，随后的 CountTokens 与统计上报由页面应用发出；请求取消时点击 Stop。同一账户同时进行的界面请求各用一个标签页。其余受保护请求的正文由 Go 编码，proof 由 WAA service 生成，通过同一固定指纹页面以官网同样的 `XMLHttpRequest` 发送，GenerateContent 带官网点击 Run 时的 `Priority: u=0`，响应同样经 `script.message` 推送给 Go。WebDriver BiDi 命令在同一连接上并发执行：每条命令带唯一 ID，后台读取连接的 goroutine 按 ID 把响应交给等待的调用方，单条命令最长 150 秒；请求取消或超时时调用立即返回，迟到的响应被丢弃，其他命令不受影响。其他 MakerSuite、Drive 与媒体控制面请求继续使用账户固定出口的 Go HTTP transport。

`WAA_BACKEND=spidermonkey` 时不定位、不下载也不启动 Camoufox。每个账户 runtime 在服务进程内请求官网页面、页面 HTML 引用的样式表与脚本、网站图标与 Cloud Console 认证预热 iframe，再请求 `GetLoggingContext` 得到公开请求头，调用 `Waa/Create` 取得 challenge，按 hash 下载并缓存解释器到 `auth/.waa-interpreters/`，在 `internal/waa` 链接的 SpiderMonkey 与 Firefox 形状宿主中执行 program，program 插入的 iframe 由宿主以 Firefox iframe 导航请求；页面子资源与解释器按 Firefox HTTP 缓存的新鲜度与条件请求规则发送，缓存元数据写入账户目录的 `spidermonkey-cache.json`；带页面提示词的 GenerateContent 先在宿主页面的输入框填入提示词并点击 Run，再生成 proof。受保护请求携带 Firefox 请求头与账户 Cookie，由账户固定出口的 Go HTTP transport 发送，响应 Cookie 写回账户状态。账户页的浏览器登录在首次使用时准备 Camoufox。

默认 `WAA_BACKEND=spidermonkey`、`WARM_WORKER_LIMIT=0`，不创建常驻热池；显式启动或刷新所需的目录页面仍占用统一活动与启动预算。两个后端都固定以 `temporary=true` 打开页面，移除 `TEMPORARY_CHAT` 与额外 Hello 预热生成。可选 Camoufox 的 BiDi 并发命令用唯一 ID 分发，取消不关闭其他调用共享的连接。读取响应遵循请求 context；UTF-8 分块最多 64 KiB，编码/BiDi 队列最多 8 块 credit，但 XHR 原生 `responseText` 仍累积完整响应。512 KiB 不是浏览器或整进程内存上限，见 [运行时内存](memory.md)。

## 3. 配置、账户和持久状态

程序从当前目录读取可选的 `.env`，进程环境变量覆盖同名配置，这些项在设置页中显示为由环境变量决定、不可修改。管理端保存设置时 `.env` 只写入与默认值不同的项；普通无效配置按默认值运行，管理页面顶部与服务日志列出问题。`API_KEY_ACCOUNTS` 是安全边界：格式错误或同一已配置 key 的重复绑定拒绝加载与保存，错误不暴露 key 原文，不回退为空绑定。`config.Load` 可在读取时精简无注释且包含旧全量键的文件，升级前备份；迁移测试只使用临时副本，不把读取正式配置当作无副作用的验证。

| 变量 | 作用 | 默认值 |
| --- | --- | --- |
| `AISTUDIO_AUTH_STATES` | 账户文件、目录或逗号分隔的多个路径 | `auth` |
| `LISTEN_ADDR` | HTTP 服务监听地址；端口被 Windows 保留（Hyper-V、WSL2 或 Docker 的 WinNAT 保留端口段）时改用其后第一个可监听的端口，管理页面顶部显示改用的地址 | `127.0.0.1:2048` |
| `PREVIEW_LISTEN_ADDR` | API 试用页 HTML 预览来源的监听地址，只提供 `/shim.html`；端口与 `LISTEN_ADDR` 不同，无法监听时改用同一主机上的空闲端口，实际地址为管理端配置中的 `active_preview_listen_addr`；管理进程重启后生效 | `127.0.0.1:2050` |
| `PROXY_API_KEY` | 公开 API 访问密钥，多个用逗号分隔 | 空 |
| `API_KEY_ACCOUNTS` | 逗号分隔，每项以最后一个 `:` 分成 `PROXY_API_KEY` 中的 key 与以 `\|` 分隔的账户 ID，该 key 的请求与文件上传只在这些账户中选号，未列出的 key 可用全部账户，不在 `PROXY_API_KEY` 中的项不起作用；保存后立即生效，同时修改 `PROXY_API_KEY` 时，管理服务重启前仍被接受的 key 沿用原来的绑定 | 空 |
| `AUTO_START` | 管理服务就绪后自动启动生成服务；失败记录 `ERROR` 日志，管理服务继续运行，账户与配置以外的原因失败时以 5 秒起、翻倍至 5 分钟的间隔重试，手动启动或停止后不再重试 | `false` |
| `REQUEST_BODY_LOG` | 保存公开 API 的 POST 请求与响应正文，各截断到 64 KiB，只保留最近 1000 条；保存后立即生效 | `false` |
| `LOG_FILE` | 同时把服务日志写入 `runtime/logs/aistudio2api.log`，单文件 10 MB 轮转；保存后立即生效 | `true` |
| `LOG_MAX_TOTAL_MB` | 日志目录总量上限，超出时删除最旧的文件，不小于 20；保存后立即生效 | `500` |
| `ADMIN_AUTH_ENABLED` | 管理员账号密码登录开关 | `false` |
| `ADMIN_USERNAME` | 管理员账号 | `admin` |
| `ADMIN_PASSWORD` | 管理员密码，开启登录时必填 | 空 |
| `PROXY` | setup 与未设置账户代理时使用的出口，多个代理用逗号分隔组成代理池；留空时生成服务使用系统代理 | 空 |
| `INIT_TIMEOUT` | 单账户初始化超时 | `2m` |
| `REQUEST_TIMEOUT` | 单次请求最大执行时间，自动续写每轮重新计时 | `30m` |
| `WARM_WORKER_LIMIT` | 常驻预热账户数，0 表示按需启动 | `0` |
| `MAX_ACTIVE_WORKERS` | 活动 Worker 容量上限，必须为正数且不小于热池目标 | `1` |
| `WARM_STARTUP_CONCURRENCY` | 同时冷启动的 Worker 数，两种后端均适用 | `1` |
| `PER_ACCOUNT_CONCURRENCY` | 单账号同时执行的请求数 | `1` |
| `ROUTING_STRATEGY` | 账户轮询 `round-robin` 或粘性优先 `fill-first` | `round-robin` |
| `UPSTREAM_CHANNELS` | 保留 Playground 与 Build 固定入口，旧 Build 代理的可用性以上游为准 | `playground,build` |
| `MODEL_MAPPING` | 逗号分隔，每项以最后一个 `:` 分成别名与模型 ID | 空 |
| `MODEL_CHANNELS` | 逗号分隔，每项以最后一个 `:` 分成模型 ID 与通道 `playground` 或 `build`；保存后立即生效 | 空 |
| `BUILD_NATIVE_NONSTREAM` | 自动路由的非流式请求优先 Build 原生单次调用；固定 URL 优先 | `true` |
| `NONSTREAM_KEEPALIVE` | 非流式请求等待结果期间每 10 秒写出一个换行 | `true` |
| `FAKE_STREAM` | 流式请求按非流式取得完整结果后再以流式输出 | `false` |
| `YOUTUBE_LINKS` | 用户消息文本中的 YouTube 链接作为视频外部媒体发送 | `false` |
| `STRIP_THOUGHT_SIGNATURES` | 发往上游前去掉对话历史中的思考签名，函数调用带 `skip_thought_signature_validator`；保存后停止再启动生成服务生效 | `false` |
| `SEARCH_MODEL_ALIASES` | 为支持搜索的模型提供 `-search` 型号，使用该型号时启用 Google 搜索；保存后停止再启动生成服务生效 | `true` |
| `GOOGLE_SEARCH` | 为 OpenAI、Anthropic、Gemini 与 Interactions 的生成请求启用 Google 搜索，包含普通型号与携带函数工具的请求；保存后停止再启动生成服务生效 | `false` |
| `ANTI_TRUNCATION` | 公开 `抗截断-` 模型变体开关；目录、生成与计数请求在管理页保存后立即使用新值，与自动续写独立 | `false` |
| `CONTINUATION_REWRITE` | 以 assistant 轮结尾的请求在不支持原生续写的模型上改写为续写请求，规则见 [协议规范](protocol.md)；保存后停止再启动生成服务生效 | `true` |
| `AUTO_CONTINUATION` | Playground 分轮自动续写；关闭时不再发下一轮，管理页保存立即生效，包括尚未触发下一轮的在途请求；与输入前缀改写独立 | `true` |
| `CONTINUATION_PROMPT` | 改写时与末尾 assistant 正文一起写入系统指令的续写提示词；键存在且为空时末尾 assistant 正文作为最后一轮 user；保存后停止再启动生成服务生效 | 内置英文续写句 |
| `FIRST_EVENT_HEDGE` | 首字超时备用请求：`off`、`auto`（各模型与通道近 24 小时原请求首字等待的 p95）或正整数秒 | `auto` |
| `FIRST_EVENT_HEDGE_MODELS` | 发起备用请求的模型 ID，逗号分隔；仍受空闲 Worker 和单账户并发预算约束 | `gemini-4-argon` |
| `WAA_BACKEND` | WAA 后端 `camoufox` 或 `spidermonkey`，旧配置 `go` 自动迁移 | `spidermonkey` |
| `MEDIA_OUTPUT` | Chat Completions 与 Messages 正文中生成媒体的引用方式 `inline` 或 `url` | `inline` |
| `MEDIA_BASE_URL` | 媒体 URL 的服务地址，`http` 或 `https`；留空时取请求的协议与 Host | 空 |

`LISTEN_ADDR` 与 `PREVIEW_LISTEN_ADDR` 使用 `host:port`，端口范围为 `1..65535`。时长和容量必须为正值，但 `WARM_WORKER_LIMIT` 允许为 0，`WARM_STARTUP_CONCURRENCY` 范围为 `1..MAX_ACTIVE_WORKERS`。代理使用 `http`、`https` 或 `socks5` origin，可带 `user:password@`；命令行 `--auth` 与 `--proxy` 覆盖启动生成服务时读取的保存值。

`GET /api/config` 与 `PUT /api/config` 同时暴露保存值和当前生效值：

| 字段 | 语义 |
| --- | --- |
| `auth_states`、`proxy`、`init_timeout`、`request_timeout` | 下一次启动生成服务时使用的保存值 |
| `warm_worker_limit`、`max_active_workers`、`warm_startup_concurrency`、`per_account_concurrency` | 保存后立即作用于当前生成服务的容量参数 |
| `waa_backend`、`build_native_nonstream` | 下一次启动生成服务时使用的 WAA 与自动路由非流式配置 |
| `routing_strategy`、`upstream_channels`、`model_channels` | 保存后立即应用的路由策略、通道启用范围与模型通道约束；固定 URL 仍优先 |
| `nonstream_keepalive`、`fake_stream` | 下一次启动生成服务时使用的非流式保活与假流式设置 |
| `youtube_links` | 下一次启动生成服务时使用的 YouTube 链接识别设置 |
| `strip_thought_signatures` | 下一次启动生成服务时使用的历史思考签名剥离设置 |
| `search_model_aliases` | 下一次启动生成服务时使用的搜索后缀型号开关 |
| `google_search` | 下一次启动生成服务时使用的全模型 Google 搜索开关 |
| `continuation_rewrite`、`continuation_prompt` | 下一次启动生成服务时使用的末尾 assistant 轮改写开关与续写提示词 |
| `anti_truncation`、`auto_continuation` | 两个独立热开关；保存后立即生效，旧客户端省略 `auto_continuation` 时保留当前值 |
| `api_key_accounts` | 保存后立即应用的 API key 账户绑定；不改变当前管理进程接受的 key 列表 |
| `first_event_hedge`、`first_event_hedge_models` | 下一次启动生成服务时使用的首字超时备用请求设置与模型范围 |
| `model_mapping` | 下一次启动生成服务时使用的模型别名映射 |
| `auto_start` | 下一次启动管理进程时是否自动启动生成服务 |
| `request_body_log` | 保存后立即决定是否保存请求与响应正文 |
| `log_file`、`log_max_total_mb` | 保存后立即开关日志文件并按新的总量上限轮转 |
| `admin_auth_enabled`、`admin_username`、`admin_password` | 保存的管理登录配置；省略密码保留现值，密码只接受写入 |
| `admin_password_set` | 是否已配置管理密码 |
| `listen_addr`、`proxy_api_key` | 保存的管理监听配置 |
| `active_listen_addr`、`active_proxy_api_key` | 当前管理进程固定使用的值 |
| `reserved_listen_addr` | 被 Windows 保留而改用 `active_listen_addr` 的监听地址，没有改用时为空 |
| `active_proxy` | 当前生成服务使用的出口，代理池为逗号分隔的全部代理，`proxy` 为空时为系统代理，没有系统代理时为空 |
| `proxy_pool` | 当前生成服务的代理池：每个代理的地址、是否可用、绑定账户数、最近检测时间与最近错误 |
| `management_restart_required` | 保存的监听地址、API key 或管理登录配置与当前管理进程不同 |
| `service_restart_required` | 保存的生成服务配置与当前生成服务实例不同 |
| `environment`、`invalid` | 只读的环境变量锁定信息与普通无效配置提示 |

`PROXY` 含多个代理时组成代理池。启动生成服务时先经每个代理访问 `https://aistudio.google.com/` 检测，检测失败的代理不参与分配；未设置账户代理的账户保留运行状态 `proxy` 中仍可用的绑定，其余账户依次绑定到绑定账户最少的可用代理并写入运行状态。添加或导入账户时，登录使用该账户将绑定的代理。生成请求或 Worker 启动经代理出现网络错误（Go 传输层错误、页面 `NetworkError`，或 Worker 载入 AI Studio 期间连不上代理）时累计到请求发出或 Worker 启动时账户绑定的代理，成功一次清零；连续 3 次且存在其他可用代理时该代理标为不可用，其账户改绑到其他可用代理，并在独占租约下按新出口重建请求头客户端与 Worker。代理池每 5 分钟重新检测全部代理，检测失败的代理标为不可用，每次检测后绑定在不可用代理上的账户改绑到可用代理，检测成功的代理重新参与新账户的分配。在管理端清空账户代理时，账户原有的代理池绑定不可用则先改绑到可用代理，再按新出口重建请求头客户端与 Worker。只有一个代理时网络错误不改绑。账户出口的优先级为账户代理、代理池绑定、系统代理、直连。

配置保存使用临时文件、`Sync` 和原子替换。监听地址、本地 API key 和管理登录配置由管理进程持有，进程重启后应用；容量、路由策略、模型通道、账户绑定、抗截断/自动续写及日志等表中注明的即时项保存后生效，其余生成配置在 Stop/Start 后应用。已有显式 `FIRST_EVENT_HEDGE=off`、请求超时与 `UPSTREAM_CHANNELS=playground,build` 不因旧默认值迁移而被删除，正数热池和用户并发配置同样保留。

管理登录开启后，`/api` 使用独立的 HttpOnly、SameSite=Strict 会话 Cookie：

- 登录有效期为 12 小时；登录时勾选“30 天内保持登录”的会话有效期为 30 天，保存在 `runtime/admin-sessions.json`，管理进程重启后仍有效
- 退出登录撤销会话并结束它的管理 SSE 订阅

登录关闭时，管理 API 使用回环来源与回环 Host 校验。远程管理通过 HTTPS 反向代理，代理保留 `Host` 并设置 `X-Forwarded-Proto: https`。生成 API 的访问密钥独立配置。

生成服务启动顺序如下。源码中的 `generation` 表示一次 Stop/Start 创建的生成服务实例：

```text
PUT /api/config
  -> 校验并原子写入 .env
  -> 返回 saved/active 差异

POST /api/control/stop
  -> 取消账户任务、LAUNCHING、活动请求与后台扩容中的 Worker 启动
  -> 等待模型目录刷新退出并关闭当前 Worker
  -> 管理监听器继续提供 /api 与页面

POST /api/control/start
  -> 完成当前已停止实例的清理
  -> 重新读取 .env 并应用命令行 --auth/--proxy 覆盖
  -> 创建并启用新的生成服务实例
  -> 从账户目录的 catalog.json 建立模型目录
  -> 有缓存目录：立即预热 WAA Worker，首个 Worker 有结果后读取全部 enabled ready/busy 账户的页面目录
  -> 无缓存目录：同时读取全部 enabled ready/busy 账户的页面目录，等待首个非空真实目录后预热 WAA Worker
  -> 首个 Worker 就绪后进入 RUNNING；可预热的账户全部处于冷却时直接进入 RUNNING，冷却结束后预热；首轮目录同步完成前没有可预热账户时，同步完成后再预热一次
  -> WARM_WORKER_LIMIT=0 时跳过常驻预热；目录页面与请求 Worker 共用预算
  -> 剩余目录同步与热池预热继续在后台运行
```

配置读取、校验、生成服务实例创建失败或启用前取消时，当前已停止实例保持不变。切换到新实例后启动失败时，该实例进入 `STOPPED`，管理端返回结构化错误。账户的 ListModels 响应与 Build 模型目录在每次成功刷新后写入账户目录的 `catalog.json`，载入账户时只采用目录指纹与 `runtime-state.json` 一致的缓存，新的生成服务实例因此可以用上次同步的目录立即预热，请求编码所需的模型条目也从缓存恢复，同时在后台刷新全部账户。预热按 `WARM_STARTUP_CONCURRENCY` 同时启动多个账户的 Worker，账户的目录就绪后立即占用空出的启动名额，不等待同批其他 Worker。没有 Worker 就绪也没有 Worker 在启动时，预热先只启动一个 Worker，它的 Camoufox 进程完成启动（开始连接 WebDriver BiDi）或启动结束后再启动其余 Worker，首个 Worker 独自通过浏览器进程的启动阶段。

模型目录刷新与当前生成服务共用 context（Go 取消信号）。启动失败、取消或 Stop 后等待刷新退出的上界为 2 秒；未完成的 lifecycle transition（启动或停止操作）最多等待 12 秒。超时与 Worker 清理错误通过 `errors.Join` 保留在同一错误链中。

每个账户目录包含：

| 文件 | 内容 | 生命周期 |
| --- | --- | --- |
| `account.json` | label、enabled、proxy、locale、timezone、auth_user | 创建或编辑账户时写入 |
| `storage-state.json` | Cookie、localStorage 与可选 Chrome OAuth/DBSC 续签材料 | 合并 `Set-Cookie` 或认证续签后原子写回 |
| `camoufox-fingerprint.json` | 账户固定的浏览器指纹、语言与时区 | 首次运行生成，空值、窗口几何、字体、语音、WebGL 与媒体设备按 Camoufox 官方启动器规则规范化；重新登录和 WAA runtime 继续复用 |
| `runtime-state.json` | 权益等级、模型资格、冷却与资源账户绑定 | 权益同步、首次模型结果或资源变化后原子写回 |
| `catalog.json` | 最近一次同步的 ListModels 响应与 Build 模型目录 | 目录刷新成功且内容变化后原子写回 |
| `camoufox-cache/` | 该账户 Camoufox 的 HTTP 磁盘缓存（上限 32 MB，JS 字节码以 zlib 压缩存放）与 Firefox 启动缓存 | WAA runtime 运行期间独占写入，随账户目录删除 |

`runtime-state.json` 的 `model_access` value 为 `{state,checked_at,reason?}`，成功状态为 `verified`；`cooldowns` value 为 `{until,reason?,level?}`，`level` 记录套餐限额的递增档位，旧状态省略时按未递增处理；`resources` value 保存 kind、name、mime、size、purpose、created_at 与可选 video 元数据。Drive file、视频任务、视频产物和 Bidi 恢复 token 保持创建账户粘性。视频任务保存公开 video object 的 model、seconds、size 与 UTC 创建时间，重启后可恢复这些字段及已绑定产物，不恢复未完成的生成。Veo 专用实现已移除，旧未完 Veo 任务不能继续轮询上游执行；已绑定文件仍按原账户读取。

活动请求锁保护跨进程账户租约，短事务锁保护 `runtime-state.json` 合并写回。短事务锁位于 `auth/.leases/<账户>.runtime.lock`，以 25ms 间隔等待，最多 2 秒；取得锁后重读磁盘状态，只修改目标字段，原子替换文件，再同步内存与资源索引。取租约时同样在锁内读取所选账户的文件，内容与上次同步或写入时相同则沿用内存状态。接收 context 的资源事务会在取消或 deadline 到达时提前返回。

账户更新以 `account.json` 原子写入为持久提交点。`internal/app` 先准备 `pending`（尚未提交）的固定出口，关闭旧 Worker并锁定该账户的 Worker 配置，再调用 `AccountLease.SaveConfig`；保存成功后依次提交 Worker 配置与固定出口。准备、关闭或保存失败时丢弃 pending 更新；保存后的租约释放错误原样返回，已发布配置继续生效。

账户调度先按每个账户实时 `ListModels` 返回的模型和方法筛选，再选择已经就绪且有并发槽位的 Worker。`ROUTING_STRATEGY=round-robin` 在每个模型的候选账户间轮询，按上次选中的账户 ID 继续；`fill-first` 持续使用 ID 排序后的首个可用账户，并在并发槽位用满、冷却或不可用时切换。带内联附件的生成请求优先选择已上传其中最多附件的账户，该账户并发槽位用满、冷却或不可用时再按策略选择。生成请求的候选为账户与 `UPSTREAM_CHANNELS` 所启用通道的组合：Playground 按 `ListModels` 与权益判断，Build 按 Build 代理返回的 Gemini API 模型目录与相同权益判断；轮询与粘性在组合间按账户 ID 与通道顺序推进，冷却按通道记录（Build 为 `build:<模型>`），一个通道冷却后同一账户可由另一通道继续，全部启用通道都冷却时按下文的冷却规则排队或返回 429；`-build` 模型只使用 Build 写代码助手通道 `assistant`，冷却 scope 为 `-build` 模型 ID；通道规格见 [Build 通道](build.md)。并发槽位、Worker 与 WAA 按账户共享；计数、Live、转录与 Drive 文件引用使用 Playground RPC。每个账号最多同时租用 `PER_ACCOUNT_CONCURRENCY` 个请求槽位；首个请求获取跨进程文件锁，最后一个请求释放。WAA proof 由账号 worker 串行生成，`GenerateContent` 由同一 Camoufox 页面并发发送并流式读取（`WAA_BACKEND=spidermonkey` 时由账户 runtime 的 Go HTTP 发送）；请求前使用 Worker 当前 Cookie 生成 Authorization，响应头到达后把 Worker Cookie 原子同步到账户持久状态。其他 MakerSuite HTTP 响应的 Cookie 在响应头到达时与最新账户状态合并。未固定账户的请求遇到可重试的 401、403、404、429、5xx、收到响应前的连接断开或单账户初始化超时时，可以在首个上游语义事件前继续切换尚未尝试的同能力账户；Drive 引用随需要临时复制到生成账户，显式账户和视频任务保持账户绑定。Chrome 导入状态保留续签材料，HTTP `401` 时在同一固定出口续签一次、重建该账户 WAA runtime 并重放请求。

Worker 容量由热池目标、活动上限和单账户并发共同约束。冷启动、临时目录页和页面重载都先预留活动与启动槽位；容量已满时，无论热池是否为零，都先关闭可回收的空闲旧 Worker 再启动替代者，不以 pending Worker 绕过峰值限制。旧实例优先选择对请求模型处于冷却的账户，再按最久未用排序。同账户重载需要独占维护窗口；只有预留了额外活动槽位才允许新旧页面短暂共存，否则先关闭旧页再重建，不侵入其他在途请求。失败、取消和关闭会释放新资源，关闭失败的实体继续占用预算并由后续 Stop 重试。

请求只在取得容量槽位时占用冷账户；没有可用槽位时释放账户并重新分类，按相同选择条件先到先服务排队，在租约或 Worker 状态变化时唤醒，直到请求超时。全部候选冷却时，最早恢复时间在 1 分钟内的请求排队，更晚的直接返回 429 并给出恢复时间。`WARM_WORKER_LIMIT=0` 时不预热，每 3 秒检查并在约 30 秒空闲后关闭 Worker；正数热池在空闲 5 分钟后关闭超出目标的 Worker。零热池同时释放空闲分词模型，仅在实际释放资源后调用 GC 归还空闲 Go 堆。WAA runtime 租约被其他进程持有时，该账户退出预热与调度候选，首次 5 秒后重新探测，仍被占用则间隔翻倍至最多 1 分钟；每段占用只记一条日志，指定该账户或只有此类账户时返回账户正在使用的错误。

容量设置保存后立即作用于当前生成服务：超过新的 `MAX_ACTIVE_WORKERS` 时关闭额外空闲 Worker，忙碌实例等待请求结束；常驻数调高时补足预热，等待中的启动与请求按新的启动并发/账户并发放行。`WARM_STARTUP_CONCURRENCY` 对 SpiderMonkey 与 Camoufox 均生效，已有 `.env` 的用户配置不被默认值覆盖。

`FIRST_EVENT_HEDGE` 不为 `off` 时，`FIRST_EVENT_HEDGE_MODELS` 列出的模型的生成请求，原请求从取得账户起等待首个语义事件达到阈值后，为该请求发起一个备用请求：自定义秒数为固定阈值；`auto` 取该模型与通道近 24 小时最近 200 个原请求首字等待的 p95，样本少于 20 个时不发起。原请求的样本为取得账户到首事件的时长，被备用请求取代的原请求按已等时长计入，备用请求不计入；管理进程启动时从请求账本补入近 24 小时的样本。备用请求先用 Worker 已就绪且没有活动请求的其他账户，没有时用原账户的其余并发槽位；引用已上传文件或指定账户的请求只用原账户的其余并发槽位；都没有时每秒复查；同时进行的备用请求最多 4 个；后台 Responses 与 Interaction、Deep Research 与 Antigravity 等经后台任务生成的 agent（`interaction_task`）与 `-build` 写代码助手请求不发起。先出首事件的一方继续，另一方取消上游请求、清理临时文件并释放租约，被取消的请求同样计入上游的每日与每分钟请求额度；账本的 `attempts` 以 `hedge` 标记备用请求，以 `superseded` 标记被取代的一方。

故障重置先等待同账户的活动请求释放租约；等待期间该账户暂停接收新请求。客户端取消只结束自身生成请求；账户后台任务需显式 DELETE。页面与 WAA 初始化就绪后才发布 Worker，不再提交 Hello 预热生成；业务请求发送前再次检查账户冷却状态。

非流式请求在提交结果前收齐上游事件，首事件之后的传输中断也可丢弃部分结果并换号；普通协议或参数错误不因此重试，流式响应在首事件后不重放。清理中断流时仍监听请求取消，避免一直占用账户槽位。公开 `/v1` 固定 Playground，`/build/v1` 固定 Build；换号、额度冷却与非流式优先选项均不能越过 URL 指定的通道，未固定通道的内部调用和 `/v1beta` 才保留原组合调度。

额度冷却按 `quota_unit`、`quota_limit`、`quota_metric`、错误文案依次判定周期。明确的每日限额在美国太平洋时间次日零点恢复；分钟限额按有效的 `window_start_time` 恢复；通用 429 冷却一分钟。没有明确恢复时间的 `reached your usage limit` 套餐限额在同一 scope 按 1、5、15、30、60 分钟递增；活跃冷却不因并发失败而延长，过期后再次失败才升档，成功清除后从 1 分钟重新开始。`RetryInfo` 与 `Retry-After` 提供的重试时间优先。所有额度项均为 `limit: 0` 时标为无配额，仍遵守明确的周期或重试时间。全局范围由额度元数据确认，其余错误只冷却失败模型或能力 scope；正文后的额度错误与 Live 会话错误使用同一规则。冷却日志只记录实际生效的期限，陈旧代际或检查时间不覆盖新状态。HTTP 错误响应按可重试时刻返回 `Retry-After`，Gemini 同时返回 `RetryInfo`。

套餐错误若明确写出 `resets in 10 hours, 2 minutes`，按该恢复时刻冷却；订阅模型遇到额度错误时后台刷新权益与目录，同一账户每分钟最多一次。管理端可手动清除某一 scope 的冷却；每日恢复仍按美国太平洋时间计算。

账户状态：

| 状态 | 含义 |
| --- | --- |
| `ready` | 认证有效且存在可调度容量 |
| `busy` | 账户存在独占操作、认证刷新或活动请求；调度仍按 `PER_ACCOUNT_CONCURRENCY` 判断剩余槽位 |
| `cooldown` | 账户的全局 `*` 冷却仍有效；模型 `scope`（状态记录范围）冷却只影响对应请求的候选分类 |
| `auth_required` | 账户级认证失败，需要重新登录或续签 |
| `unavailable` | 当前运行时无法使用账户 |
| `disabled` | 账户配置已停用 |

认证结果携带 `authGeneration`（认证状态版本）与 `checkedAt`（检查时间），仅在账户对象、authGeneration 和时间顺序均匹配时应用；同一时间点的成功结果优先于失败。模型成功与冷却写回使用独立 `modelAccessGeneration`（模型状态版本）和 `checked_at`，目录变化时 modelAccessGeneration 递增，同一时间点已有 `verified` 时保留成功状态。

模型访问 scope：

| 操作 | scope | 成功与失败语义 |
| --- | --- | --- |
| 普通 GenerateContent | `<modelID>` | 规范 `EventFinish` 到达后写 `verified`；Code 7 保留已有记录 |
| CountTokens | `count-tokens:<modelID>` | 成功清该 scope 冷却，模型 `verified` 保持原值 |
| Transcribe | `<modelID>` | 非空文本或 segments 写 `verified`；Code 7 保留已有记录 |
| Live 纯文本 | `<modelID>` | setup 成功写 `verified`；每次 `SendText` 开始一次模型资格检查，`turn_complete` 更新成功 |
| Live 音频或图像 | `bidi-media:<modelID>` | `SendMedia` 开始一次模型资格检查并更新媒体 scope |
| Robotics | `bidi-media:<modelID>` | `SendText` 开始一次模型资格检查，`turn_complete` 更新成功 |

`ModelAccessKey(scope, model)` 会移除 `models/` 前缀；空 scope 返回规范模型 ID，非空 scope 返回 `<scope>:<canonicalModelID>`。Bidi setup 使用 lease（账户租约）时间，会话内每个 qualifying turn（一次模型资格检查）分配严格递增的 attempt 时间，`turn_complete` 消费对应 attempt。普通流式生成在规范 `EventFinish` 到达时写 `verified`；此前的 text、reasoning、tool、usage 和首事件用于输出与性能统计，终态前断流、取消或错误保持原验证状态。

模型目录通过 `PageCatalogProvider.PageCatalog(ctx, accountID, reload)` 读取账户页面载入的 `GetLoggingContext`、`GetAiStudioBenefitTier` 与 `ListModels` 响应。有可复用 Worker 时读取当前目录，显式刷新可重载页面；新建临时页、冷启动与重载共用 `MAX_ACTIVE_WORKERS` 和 `WARM_STARTUP_CONCURRENCY`，满载时等待、重试或安全回收，不另开预算外页面。HTTP Cookie 导入只提交认证，不触发这条目录路径。

启动期同步对全部 enabled ready/busy 账户执行，缺少目录的账户可并发排队，已有目录的逐个读取，每项占用普通账户槽位并受上述页面预算约束。错误或空目录加入 generation 的 pending 集合；非空结果立即更新公共目录与账户状态，正数热池在 RUNNING 时继续预热。启动期结束后以单个 30 秒 ticker 重试 pending 账户，并从该轮开始每 24 小时逐个刷新全部可用账户，槽位已满的跳过。请求模型不在目录且距上轮开始超过 10 分钟时可触发一轮刷新，请求等待首个账户结果，其余在后台继续。冷却项到期再试，需要登录、停用、不可用或已删除的账户退出重试；失败记录原因，成功记录模型数量。`modelRevision` 确保服务接收请求前已应用当前账户和配置版本。

认证状态包含长期凭证和设备绑定材料，保存在本机受控目录。提交、Issue、CI 和普通日志使用脱敏材料，保留字段形状并替换 Cookie、token、proof、邮箱、账户 ID、提示词、响应正文和完整原始帧。

## 4. Go 协议层、公开端点与 Vue 管理端

公开端点统一读取同一份实时模型目录和规范事件：

| 协议 | 端点 |
| --- | --- |
| OpenAI Chat | `GET /v1/models`、`GET /v1/models/{model}`、`POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses`、`POST /v1/responses/input_tokens`、`POST /v1/responses/compact`、`GET/DELETE /v1/responses/{id}`、`GET /v1/responses/{id}/input_items`、`POST /v1/responses/{id}/cancel` |
| Gemini Interactions | `POST /v1beta/interactions`、`GET/DELETE /v1beta/interactions/{id}`、`POST /v1beta/interactions/{id}/cancel`，`/v1/interactions` 下路径相同 |
| OpenAI 与 Anthropic Files | `POST/GET /v1/files`、`GET/DELETE /v1/files/{file}`、`GET /v1/files/{file}/content` |
| OpenAI 媒体 | `POST /v1/images/generations`、`POST /v1/images/edits`、`POST /v1/audio/speech`、`POST/GET /v1/videos`、`GET/DELETE /v1/videos/{id}`、`GET /v1/videos/{id}/content` |
| OpenAI Transcribe | `POST /v1/audio/transcriptions`、`POST /v1/audio/translations` |
| OpenAI Embeddings | `POST /v1/embeddings` |
| Anthropic | `POST /v1/messages`、`POST /v1/messages/count_tokens` |
| Gemini | `GET /v1beta/models`、`GET /v1beta/models/{model}`、`POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent`、`:countTokens`、`:embedContent`、`:batchEmbedContents`、`:predictLongRunning`、`GET /v1beta/operations/{id}` |
| Gemini Files | `POST /upload/v1beta/files`、`GET /v1beta/files`、`GET/DELETE /v1beta/files/{file}` |
| Realtime | `GET /v1/live`、`GET /v1/robotics/stream` |

管理端路由：

| 能力 | 路由 |
| --- | --- |
| 健康与状态 | `GET /health`、`GET /api/status` |
| 模型与账户 | `GET /api/models`、`GET/POST /api/accounts`、`GET/POST /api/accounts/import/chrome`、`GET /api/accounts/import/browsers`、`POST /api/accounts/import/cookies/discover`、`POST /api/accounts/import/cookies`、`PUT/DELETE /api/accounts/{id}` |
| 账户任务 | `GET /api/accounts/tasks`、`DELETE /api/accounts/tasks/{id}` |
| 登录与刷新 | `POST /api/accounts/{id}/login`、`POST /api/accounts/{id}/refresh`、`GET /api/login-view/{view}`（WebSocket） |
| 远程配对 | `GET /api/pairing`、`POST /api/pairing`、`POST /api/pairing/accounts`、`PUT /api/pairing/accounts/{account}/cookies` |
| 生成服务 | `POST /api/control/start`、`POST /api/control/stop` |
| 配置 | `GET /api/config`、`PUT /api/config` |
| 冷却与请求 | `GET /api/cooldowns`、`GET /api/requests`、`POST /api/requests/{id}/cancel` |
| 用量与正文 | `GET /api/usage`、`GET /api/usage/records`、`GET /api/usage/records.csv`、`GET /api/requests/{id}/body` |
| 日志与事件 | `DELETE /api/logs`、`GET /api/events` |
| 在线更新 | `GET /api/update`、`POST /api/update` |

`/api` 接受 loopback 请求，并在请求带 `Origin` 时执行 same-origin 校验。`POST /api/pairing/accounts` 只校验配对令牌，`PUT /api/pairing/accounts/{account}/cookies` 只校验续签凭据。`/v1` 与 `/v1beta` 使用公开 API key 与 CORS。

OpenAI Responses 的 `previous_response_id` 与 Gemini Interactions 的 `previous_interaction_id` 共用最多 256 个响应节点，用于重建下一轮完整 contents；生成期间上一轮被淘汰时，新响应保存请求开始时读取的完整上下文。已保存的终态节点写入 `runtime/states.db`，保留范围内的 ID 在服务或进程重启后可查询与续接，未保存、已删除或淘汰的节点不可恢复。进行中的生成任务、账户任务及内存 Build 对话映射不恢复。Drive、视频任务与产物文件的账户绑定写入 `runtime-state.json`，已完成产物可继续读取，但未完成视频不会因资源绑定持久化而继续执行。

新增上游能力从 `internal/aistudio` 开始：编码真实数组槽位、解码服务器事件，再由 `internal/api` 投影到公开协议。模型方法、上下文、输出上限、工具、声音、图片规格和视频规格均来自实时 `ListModels`。

`runtimeManager` 同时实现基础 `Service` 以及 Video、File、Bidi 和 Transcription 扩展接口。`internal/app` 负责资源账户绑定、跨账户文件复制、运行状态和重试流程；`internal/api` 负责 HTTP、SSE 与 WebSocket DTO，即公开协议使用的请求、响应和事件对象。完整请求字段、响应 DTO、SSE 和 WebSocket 事件见 [protocol.md](protocol.md)。

前端开发命令：

```powershell
cd web
npm run dev
npm run typecheck
npm run test
npm run lint
npm run format:check
npm run build
```

前端工具链为 Vite+：`vite.config.ts` 同时配置开发服务、构建、Oxlint 与 Oxfmt。

直接运行 `npm run dev` 时，API 代理默认指向 `http://127.0.0.1:2048`；可以通过进程环境变量 `AISTUDIO_DEV_API_TARGET` 修改。隔离开发实例使用 `12048` 时须显式设置该变量，详见 [Windows 开发服务器](#windows-开发服务器)。

- `npm run lint` 先运行 Oxlint，再运行 ESLint；ESLint 只负责 eslint-plugin-vue 的 `recommended` 模板规则，`<script lang="ts">` 由 `@typescript-eslint/parser` 解析，与 Oxlint 共有的规则由 eslint-plugin-oxlint 关闭，与格式化冲突的格式规则由 eslint-config-prettier 关闭
- `npm run typecheck` 由 vue-tsc 检查脚本与模板类型
- `npm run format:check` 由 Oxfmt 检查格式

Vite+ 将生产产物写入 `internal/webui/dist`，脚本、样式与 SVG 以 gzip 压缩后只保留 `.gz` 文件，`index.html` 与字体保持原样；KaTeX 经 `web/src/playground/katex.scss` 只打包 woff2 字体。管理端通过本机 `/api` 路由管理生成服务、日志、账户、配置、模型冷却、活动请求和 SSE 状态事件；认证状态与 WAA 对象不进入浏览器存储。

管理端提供简体中文、繁體中文、English、日本語、한국어、Français 与 Deutsch，所选语言保存在浏览器的 `user_lang`，未选择时按浏览器首选语言选择，都不支持时为 English。文案位于 `web/src/locales/<语言>.ts`，`zh-CN.ts` 定义全部键，其他语言按同一键集合提供 `messages`，缺键时类型检查失败；每种语言打包为单独的文件，页面只加载当前语言与切换到的语言。管理接口错误体的 `error.code` 对应 `error.<code>` 文案，`{detail}` 填入服务返回的 `error.message`，未收录的错误码显示服务原文。

服务生成的日志、错误、账户状态、冷却原因与命令行输出以简体中文写成，`internal/i18n/messages/<语言>.json` 是其他六种语言的翻译表：键为源码中含中文的字符串字面量与 Go 格式串，值为译文，`{0}`、`{1}` 依次对应格式动词。新增或修改含中文的文案时，在六个翻译表中写入同一键的译文。`internal/i18n` 与 `web/src/messages.ts` 按同一规则翻译：整句匹配格式串，格式串开头的格式动词取最长匹配，格式动词的取值递归翻译；整句没有匹配时依次按换行、` | `、`; `、`；` 分段，按第一个 `: ` 或 `：` 拆成外层说明与内层原因，按不含格式动词的文案前缀拆开，最后拆开 `键=值`。管理端各语言的界面文案 `web/src/locales/<语言>.ts` 引入同一语言的翻译表，两者打包在同一个文件中，按界面语言显示服务文案；控制台与命令行按操作系统语言输出，日志文件保持简体中文。上游原文、账户、模型名与数值保持原样。

`internal/webui/embed.go` 使用 `//go:embed dist`，因此 Go 构建前必须生成当前前端产物；静态文件由 `statigz` 返回，接受 gzip 的客户端直接收到 `.gz` 内容，其余客户端收到解压后的内容；`/assets/` 下文件名带内容哈希的静态文件带 `Cache-Control: public, max-age=31536000, immutable`，其余页面与静态文件带 `Cache-Control: no-store`，对带 `If-None-Match` 的请求也返回完整正文；入口页的内联脚本在资源载入失败、入口模块解析失败或 `vite:preloadError` 时以 `cache: reload` 重新取回本页的 `/assets/` 资源后重载，30 秒内只重试一次；`/api` 的 JSON、CSV 与事件流响应同样以 gzip 返回给接受 gzip 的客户端，事件流每个事件写出后随即刷新。管理端从 `/api/events` 接收 `status`、`models`、`accounts`、`account_tasks`、`log`、`cooldowns` 和 `request` 事件。

API 试用页位于 `web/src/playground/`，路由与官网相同（`/prompts/new_chat`、`/prompts/new_image`、`/prompts/new_video`、`/generate-speech`、`/new_music`、`/live`、`/prompts/:id`）。`request.ts` 由对话轮次与运行设置组成 Gemini `generateContent` 请求，`protocols.ts` 把同一请求转换为 OpenAI Chat、OpenAI Responses、Anthropic Messages 或 OpenAI Images，并以 `eventsource-parser` 读取各协议的 SSE，以各协议完成事件结束请求；流内错误与提前断流显示为失败。响应头、正文和心跳的刷新错误沿 HTTP 写入路径返回，事件转发在取消时释放账户租约。停止后可继续提交；运行快捷键在 User settings 中选择 Ctrl + Enter（默认）或 Enter。回复正文由 `markdown-it` 按 Markdown 渲染，原始 HTML 按文本显示。对话保存在 `runtime/prompts.db`，经 `/api/prompts` 读写，在轮次、标题或运行设置变化后合并保存；历史页的重命名经 `PATCH /api/prompts/{id}` 只改写标题与描述，复制经 `POST /api/prompts/{id}/copy` 在服务端完成。与官网一样，对话页在未命名时于运行结束后以最后一轮用户文本与模型回答、视频、音乐与 Imagen 页在提交时以提示词经 `POST /api/prompts/title`（`GenerateTitle`）生成标题，失败时保持 Untitled prompt。

运行设置按模型目录的能力字段显示，只发送界面上可见且已填写的参数，未填写的数值参数由模型默认值决定；所选协议无法表达的设置（`protocols.ts` 的 `protocolSupports`）在该协议下隐藏。图片、视频、语音、音乐与实时对话页位于 `media/`：图片经 Gemini `generateContent` 或 OpenAI Images，视频经 Gemini `predictLongRunning` 或 OpenAI Videos，语音经 `/v1/audio/speech` 或 Gemini 端点（返回的 `audio/L16` 在页面封装为 WAV），实时对话经 `/v1/live` WebSocket；宽高比、分辨率与时长取自模型目录的 `capability_options`。

日志页保留最近 2000 条事件，按请求与运行阶段合并后先显示最近 200 行。关闭自动滚动时，当前首行保持固定，新日志追加在末尾。请求详情与阶段步骤在首次展开时渲染。

管理面板默认打开日志页，不是账户页；侧栏折叠与账户地区不预填保持现有行为。抗截断 normal view 只在公开响应和用量展示层隐藏保留前缀，不修改实际目录与审计请求名。`requested_model` 保留原始请求，`response_model` 记录响应模型；用量展示只移除一次 `抗截断-`，保留 `models/` 与模型后缀。工具调用数来自实际观察到的内部传输工具和普通函数调用，不按抗截断开关固定填 1，内部工具名和统计字段不泄漏到生成响应，详见 [协议规范](protocol.md#抗截断与普通响应视图) 与 [用量账本](logging.md#用量账本)。

“接入”页由 `web/src/access/` 提供客户端配置、按实时模型能力生成的 SDK/cURL 示例与端点表，复用试用页的协议转换及固定 URL 选择。Build-only / assistant 模型必须保留 `/build/v1`，不能因新接入页统一写死 `/v1`。Agent 页按目录的 `interaction_task` 与 Interaction 配置显示 Deep Research / Antigravity 的工具及模型设置，支持范围取决于所选 Agent 和公开协议，不承诺所有客户端工具都由服务器执行。

## 5. 协议实现

新增上游能力按以下顺序实现：

1. 在 `internal/aistudio` 定义规范请求、响应类型与模型能力
2. 编码 [protocol.md](protocol.md) 中对应的 JSON+protobuf 数组
3. 将网络增量解码为规范事件
4. 由 `internal/api` 分别投影 OpenAI、Responses、Anthropic 和 Gemini 协议，响应的块、字段与顺序与各协议官方服务的输出一致
5. 将结构化状态与操作入口绑定到管理端

`internal/api` 消费规范请求与事件，`internal/aistudio` 管理账户文件、WAA runtime、原始上游数组和实时模型能力。资源型操作在创建时记录账户 ID，后续轮询、下载与提示引用使用该账户。

已识别字段校验类型和 oneof 约束。未知非空槽保留为 provider 事件或原始扩展字段；无法解释的已消费字段和无效媒体内容返回结构化协议错误；缺失完成帧报告为上游流中断。

## 6. 构建与贡献

本地数据库使用经 cgo 编译的 SQLite，`internal/waa` 经 cgo 链接 SpiderMonkey 静态库，构建需要 C/C++ 编译器与当前平台的静态库：Windows 为 llvm-mingw 的 UCRT 版 clang，macOS 为 Xcode Command Line Tools，Linux 命令行版为 Zig、桌面版为 gcc；静态库来自 [Mag1cFall/firefox Releases](https://github.com/Mag1cFall/firefox/releases)，下载与 `CC`、`CXX`、`CGO_CXXFLAGS`、`CGO_LDFLAGS` 的设置见 README 的源码构建一节。发布二进制前先构建前端：

```powershell
cd web
npm ci
npm run build
cd ..
go build -trimpath -o aistudio2api.exe ./cmd/aistudio2api
```

提交前执行与修改相关的功能验收、前端检查及 Go 静态检查：

```powershell
go vet ./...
```

桌面版以构建标签 `desktop` 编译，`internal/desktop` 用 Wails v3 创建窗口与系统托盘，发布构建同时加 `production` 标签。Linux 桌面版另加构建标签 `gtk3`，需要 `libgtk-3-dev` 与 `libwebkit2gtk-4.1-dev`，链接 glibc 版静态库 `linux-amd64-gnu`：

```powershell
go build -trimpath -tags desktop,production -ldflags "-H windowsgui" -o aistudio2api.exe ./cmd/aistudio2api
```

```bash
CGO_ENABLED=1 go build -trimpath -tags desktop,production,gtk3 -o aistudio2api ./cmd/aistudio2api
```

上面的 Bash 命令用于 Linux，macOS 不加 `gtk3`。Linux 的主窗口与托盘菜单把 WebKitGTK 的硬件加速策略设为 `WebviewGpuPolicyNever`，页面由 CPU 绘制，不经 GL 合成。Linux 启动时检查会话总线的 `org.kde.StatusNotifierWatcher`；没有托盘宿主（例如未安装托盘扩展的 GNOME）时关闭窗口即退出，宿主之后出现时再创建托盘。

Windows 桌面版启动时经 Wails 查询 WebView2 运行时版本。查不到且以显示窗口方式启动时，用消息框询问是否安装，询问的同时在后台下载微软 Evergreen 引导程序：确定后等下载完成，后台下载失败时重新下载一次，以 `/install` 按当前用户权限安装，下载与安装进度显示在引导程序自己的窗口中，完成后创建桌面窗口；取消询问时删除已下载的引导程序。在引导程序窗口中取消安装与取消询问相同，安装失败时先以消息框说明。取消、安装失败或以隐藏方式启动时只创建托盘：服务就绪后在默认浏览器打开管理页面，双击托盘图标或再次运行可执行文件也打开管理页面。

有托盘时，主窗口在显示时创建，关闭时连同 WebView 一起销毁，进程保留托盘；再次运行或从托盘显示窗口时，以上次的位置、大小与最大化状态重建并打开上次管理路由。页面经 `busy:1`、`busy:0` 告知桌面壳是否需要保留：有生成或管理写请求、待保存或保存失败的对话、未发送输入、未保存设置，或只在页面内的生成图片与音频时，关闭只隐藏窗口，空闲后再销毁。Linux 无托盘宿主时采用退出行为，不依赖不可见的托盘入口。托盘菜单在页面加载前按系统语言显示，之后跟随界面语言。

Windows 托盘菜单是 `tray_windows.go` 以 Win32 GDI 绘制的弹出窗口：右键或用键盘打开托盘图标菜单时，以 `Shell_NotifyIconGetRect` 取得的图标位置为排除区域贴着图标弹出，并限制在所在显示器的工作区内；配色与管理页面下拉框相同，页面经 `theme:dark`、`theme:light` 消息告知界面主题，页面加载前跟随系统深浅色；字号与间距按所在显示器的 DPI 缩放；方向键、Home、End 移动高亮，回车与空格选择，Esc 或失去焦点时关闭。macOS 与 Linux 通过 Wails 小窗口渲染 `tray.html`，使用相同的菜单配色、行高和键盘操作，关闭菜单时销毁窗口。两平台按系统设置的双击间隔显示主窗口，Linux 通过 StatusNotifierItem 的 `ContextMenu` 事件打开自绘菜单。

托盘菜单的“开机自启动”每次打开菜单时从系统读取勾选状态，设置失败时以对话框显示错误：

- Windows：写入 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 的 `AIStudio2API` 值，命令为带引号的当前可执行文件加 `-open-ui=false`；值指向其他可执行文件或被任务管理器在 `Explorer\StartupApproved\Run` 中停用时显示为关闭，开关时删除 `StartupApproved\Run` 中的记录
- Linux：写入 `~/.config/autostart/aistudio2api.desktop`，`Exec` 为当前可执行文件（从 AppImage 运行时为 AppImage 文件）加 `-open-ui=false`；指向其他程序或含 `Hidden=true`、`X-GNOME-Autostart-enabled=false` 时显示为关闭
- macOS：经 Wails 注册登录项，macOS 13 及以上从 `.app` 运行时使用 SMAppService，登录后打开窗口；其余情况写入带 `-open-ui=false` 的 LaunchAgent

应用图标与打包配置在 `build/`：`appicon.svg` 为图标源文件，`appicon.png`、`windows/icon.ico` 与 `darwin/icons.icns` 由 `wails3 generate icons` 从它生成。`cmd/aistudio2api/rsrc_windows_amd64.syso` 包含 Windows 图标、清单与版本信息，由以下命令生成，`go build` 构建 Windows amd64 时自动嵌入：

```bash
wails3 generate syso -arch amd64 -icon build/windows/icon.ico -manifest build/windows/wails.exe.manifest -info build/windows/info.json -out cmd/aistudio2api/rsrc_windows_amd64.syso
```

默认 SpiderMonkey 运行不准备 Camoufox，浏览器登录、浏览器 Cookie 验证或选择 Camoufox 后端时才按需准备；HTTP Cookie 验证不启动浏览器。贡献内容聚焦单一功能或协议变更，并使用脱敏后的请求与响应样例。

GitHub Actions 对 `main`、`v*` 标签和 Pull Request 执行前端 typecheck、test、lint、format:check、build，以及 Go 单元测试与 vet。前端使用 Node.js 24.15/npm 11，产物在各 Go 任务间共享；Linux、Windows 与 macOS 原生任务经 `.github/actions/spidermonkey` 下载对应目标静态库，并缓存 CGO 编译产物。发行包和镜像注入标签或提交版本，保留 fork 更新链路：

- Windows amd64：桌面版 `aistudio2api-<版本>-windows-amd64.zip`，保留同名顶层目录，其中有 `aistudio2api.exe` 与 launcher `start.bat`；链接启用 `--gc-sections` / `--icf=safe`，7z 使用 ZIP Deflate 压缩
- macOS：arm64 与 amd64 合并的桌面版，ad-hoc 签名的 `AIStudio2API.app` 打成 zip 与 LZMA 压缩的 dmg
- Linux amd64：Ubuntu 22.04 / GTK 3 / WebKit2GTK 4.1 构建的 `aistudio2api-<版本>-linux-amd64.AppImage` 与 `.deb`
- Linux amd64/arm64 与 macOS amd64/arm64：`aistudio2api-<版本>-<系统>-<架构>.tar.gz`，tar 保留同名顶层目录，其中只有 `aistudio2api`；外层由 7z 生成 gzip，不是 7z 格式

Linux 命令行版与 Docker 镜像中的程序由 `zig cc` 以 musl 静态链接，不依赖系统 C 库；从源码构建镜像时 npm、Go 模块与编译缓存经 BuildKit 缓存挂载在多次构建间复用；Windows 版在 Ubuntu 上经 llvm-mingw 交叉编译；macOS 各包在 macOS 构建机上编译，两个架构并行。

发布流程：

- 推送 `v*` 版本标签后自动创建 Release 并附上这些发布包；含 `-` 的标签发布为预发布版本
- `main` 和 `v*` 标签均构建 `browser` 与 `no-browser` 两种 linux/amd64、linux/arm64 镜像，共四个原生 runner 任务。CI 先下载同次 `build-linux` 的对应架构发行包，再通过 Dockerfile 的 `release-browser` / `release-no-browser` 装入静态程序，不重复编译前端或 Go。程序已注入 tag 或 7 位提交 SHA；本地源码构建仍使用 `browser` / `no-browser` 目标与 `VERSION` 参数，默认 target 为 `browser`
- 两种镜像共用证书与时区层；无浏览器目标跳过 Camoufox 下载和 GUI 依赖，内置浏览器目标保留 Windows 字体、fontconfig、Xvfb 与软件 GL，删除 macOS 字体和 fontconfig。Compose 不固定 `TZ`，默认 UTC，保留管理登录及缺少 `ADMIN_PASSWORD` / `PROXY_API_KEY` 拒绝启动的校验
- 两种版本的 GitHub Actions 层缓存、digest artifact 和 manifest 发布任务相互隔离，镜像推送到本仓库 GHCR（本 fork 为 `ghcr.io/aueron1618/aistudio2api`）。`main` push 发布 `latest` 与 `main-<6位SHA>`，无浏览器版追加 `-no-browser`，包括 `latest-no-browser`；正式 tag 发布 `vX.Y.Z` / `X.Y.Z` 并更新 latest，预发布不更新 latest。当前工作流不生成裸 `main` / `main-no-browser` 标签，latest 也会被 main 推送覆盖
- 每个架构在发布 digest artifact 前以 `--network none --read-only` 容器运行 CLI 帮助和镜像内容检查；两种镜像都检查证书与时区，内置版校验 Windows 字体与 Xvfb、确认 macOS 字体已删除，无浏览器版确认未设置 `CAMOUFOX_PATH`、没有浏览器及 GTK/Xvfb/Mesa 包。每种 manifest 必须恰好收集本变体的两个架构 digest 才发布，避免混入另一种版本
- 普通构建产物在 Actions 中保留七天，Release 附件长期保留

Docker 下载的 artifact 名为 `packages-linux-<arch>`，内容为该架构的 `.tar.gz`；解包时只去掉一层目录，程序落在 `release/linux-<arch>/aistudio2api`，与两个 `release-*` 目标的 COPY 路径一致。发布程序内的版本为 tag 或 7 位 SHA；镜像 `main-<6位SHA>` 是独立的镜像标签格式。

管理端“版本”页的在线更新由 `internal/app/update.go` 实现：

| 步骤 | 行为 |
| --- | --- |
| 读取版本 | `GET /api/update` 读取 `https://api.github.com/repos/aueron1618/AIStudio2API/releases/latest`，结果缓存一小时，`?refresh=1` 立即重新读取；请求经 `PROXY` 的第一个代理，未设置时经系统代理 |
| 更新方式 | 链接时写入更新管理方的构建为 `managed`；非正式语义版本（含预发布、构建元数据或提交 SHA）为 `development`；正式版本在容器标记存在时为 `container`，其他受支持平台为 `binary`，包括 Linux 桌面 AppImage 与 deb |
| 下载与校验 | `POST /api/update` 只在 `binary` 且有更新版本时执行，按当前系统与架构下载 `aistudio2api-<版本>-<系统>-<架构>.zip`（Windows）或 `.tar.gz`，macOS 桌面版下载 `aistudio2api-<版本>-darwin-universal.zip`，Linux 桌面版下载 `aistudio2api-<版本>-linux-<架构>.AppImage` 或 `.deb`，与 Release 资产的 `digest` 核对一致后取出其中的可执行文件或 `AIStudio2API.app` |
| 替换 | `minio/selfupdate` 替换当前可执行文件，失败时恢复原文件；Windows 上原文件改名为 `.aistudio2api.exe.old` |
| 重启 | 返回 202 后按收到 SIGTERM 的顺序停止 HTTP 服务、生成服务与 Camoufox（HTTP 服务最多等待进行中的连接 10 秒，之后断开），再以原参数加 `-open-ui=false` 启动新程序：Windows 上新进程沿用当前控制台，其他系统以 exec 替换当前进程 |
| 桌面版替换与重启 | Windows 与 macOS 桌面版经 Wails 更新器把新版本暂存到系统临时目录，返回 202 后启动更新辅助进程并按上一行的顺序退出；辅助进程等待原进程结束（最多 30 秒），替换可执行文件或 `AIStudio2API.app`，启动新版本后删除备份与暂存文件 |
| Linux 桌面版替换与重启 | 从 AppImage 运行时把新版本写到 `APPIMAGE` 所在目录后改名覆盖原文件；deb 安装时经 `pkexec apt-get install` 安装，桌面环境弹出管理员认证；返回 202 后启动等待原进程结束的 `sh`，按收到 SIGTERM 的顺序退出，原进程结束后以无参数启动 AppImage 文件或 `/usr/bin/aistudio2api`，与 Windows 和 macOS 一样显示主窗口 |

`managed`、`development`、`container` 与已是最新版本时 `POST /api/update` 返回 409；页面每秒读取 `/api/status`，版本变化或返回 401 时刷新。

分发方从源码编译时用两个链接参数写入版本号与更新管理方：

```bash
go build -trimpath -ldflags "-X github.com/Mag1cFall/AIStudio2API/internal/app.version=v0.2.14-magpie.1 -X github.com/Mag1cFall/AIStudio2API/internal/app.managedBy=Magpie" -o aistudio2api ./cmd/aistudio2api
```

| 参数 | 作用 |
| --- | --- |
| `internal/app.version` | 管理端与 `/api/status` 显示的版本号，不写时读取 Go 构建信息 |
| `internal/app.managedBy` | 更新管理方名称；写入后更新方式为 `managed`，`GET /api/update` 只返回当前版本与 `managed_by`，不访问 GitHub，`POST /api/update` 返回 409，“版本”页显示更新由该分发方管理 |


源码提交包含协议实现、前端源码和公开文档。本机账户状态、Cookie、token、proof、提示正文、响应正文和运行产物留在本机。
