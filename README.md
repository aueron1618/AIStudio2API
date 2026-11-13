<div align="center">

# AI Studio to OpenAI, Anthropic & Gemini Compatible API

<p align="center">
  <a href="README.md"><b>中文</b></a>
  &nbsp;|&nbsp;
  <a href="README_en.md">English</a>
</p>

<p>
  <b>一个基于 Go 的高性能代理服务</b><br>
  将 Google AI Studio 网页协议转换为 OpenAI、Responses、Anthropic 和 Gemini 兼容 API
</p>

<p>
  Playground 生成通道 &nbsp;•&nbsp;
  多账户高并发<br>
  Camoufox 与 SpiderMonkey 双 WAA 后端 &nbsp;•&nbsp;
  Claude Code、Codex 等 Agent 客户端 &nbsp;•&nbsp;
  Nano Banana、TTS 与 Omni
</p>

</div>

**三步开始使用**：下载 [本 fork 的 Releases](https://github.com/aueron1618/AIStudio2API/releases) 并启动 → 在“账户”页添加 Google 账户 → 点击“启动服务”，把 `http://127.0.0.1:2048` 作为 OpenAI、Anthropic 或 Gemini 客户端的 Base URL


---

## 核心能力

- **固定通道入口**: 本 fork 的 `UPSTREAM_CHANNELS` 默认保留 Playground 与 Build；`/v1` 固定 Playground，`/build/v1` 固定 Build，不跨通道回退。Build 应用代理是旧通道，上游已停用其生成，返回 `PROXY_GENERATION_DISABLED` 时保留上游原因；免费账户的两个通道共用同一模型的每日请求次数
- **多账户高并发**: 识别 Free、Pro、Ultra 与 Plus 权益，按实时模型目录在账户间轮询或优先复用
- **低内存与两种 WAA 后端**: 默认由进程内 SpiderMonkey 生成官方 proof，不常驻浏览器；零热池按需启动，空闲 30 秒后释放 Worker。仍可选择 Camoufox 后端，旧配置 `WAA_BACKEND=go` 自动迁移为 `spidermonkey`
- **四套 API 协议**: OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 与 Gemini GenerateContent；Responses 支持 `/v1/responses/compact` 与 `compaction`、`compaction_trigger` 输入项的上下文压缩，Anthropic Messages 支持 `context_management` 的工具结果与思考清理和阈值压缩
- **客户端接入页**: “接入”页按客户端、能力和端点生成配置与调用示例，支持 Claude Code、Codex、OpenCode、pi、omp、dsh、OpenClaw、Hermes、Magpie 等客户端；工具调用由客户端执行，搜索与媒体支持取决于所选模型、协议和通道

## 特性

- **原生流式响应**: 实时输出正文、思考摘要、函数调用、Google 工具、媒体和 usage
- **函数工具**: 四协议支持工具调用与续接、工具选择和严格参数校验；支持联合类型与开放参数定义
- **可选抗截断**: `ANTI_TRUNCATION=false` 默认关闭；开启后可选择 `抗截断-` 模型变体，通过内部函数承载正文，再向客户端还原普通响应。与 `AUTO_CONTINUATION` 分轮续写开关独立，不保证消除上游截断
- **TTS 语音生成**: 支持 Gemini TTS 模型的单/多说话人音频生成
- **图片生成**: 支持 Nano Banana 图片生成
- **视频生成**: 视频长任务与图片转视频由 Gemini Omni 生成；Gemini Omni 通过四套生成接口接收文本、图片与视频输入，输出文本与 MP4 视频
- **Deep Research**: Pro 与 Ultra 账户可通过四套生成接口与 Interactions 的 `agent` 字段调用 Deep Research，输出研究过程、带引用的报告与图表
- **Agent 与 Antigravity**: 实时目录中的 `interaction_task` Agent 经上游后台 Interaction 运行；试用页提供目录支持的工具与设置，Antigravity 的模型选择取自其目录配置，不把普通模型或 Build 路由强制改成 Agent
- **决策模型**: `gemini-decision-1` 经 `/v1beta/decisions` 对同一份输入一次回答多个是非、单选与评分问题，返回校准后的概率与置信度；输入可以是文本、图片、音频、视频与 PDF，只计输入 token；AI Studio 官网前端尚未调用这个模型，服务经网页的 CreateDecision 请求调用
- **YouTube 输入**: YouTube 视频 URL 作为外部视频附件读取；开启 `YOUTUBE_LINKS` 后，消息文本中的 YouTube 链接同样作为视频读取
- **视频理解设置**: Gemini 与 Interactions 支持逐视频的起止时间、0–24 FPS 范围内的正数采样率和静态/Agentic 处理；Agentic 返回服务端处理步骤并支持历史续接
- **智能模型切换**: 从 AI Studio 实时发现模型并按 `model` 字段路由；开启 `DISCOVER_HIDDEN_MODELS` 后，官网前端已配置、账户模型列表尚未列出的模型经上游确认可用后同样可以调用
- **Google 工具**: 支持 Search、Image Search、URL Context、Code Execution 和 Maps
- **Files 与 Transcribe**: 支持文件上传、查询、内容读取、删除和音频转录
- **Live 与 Robotics**: 通过 WebSocket 支持文本、音频、JPEG、媒体结束、工具调用、恢复和中断
- **反指纹检测**: 为每个账户固定 Firefox 指纹与出口，官方 WAA 在 SpiderMonkey 的 Firefox 页面环境或 Camoufox 页面中运行
- **图形界面启动器**: 通过网页管理账户、服务启停、实时日志、模型、请求、用量统计和配置，界面提供深色与浅色两种配色，以及简体中文、繁體中文、English、日本語、한국어、Français 与 Deutsch 七种语言
- **桌面版**: Windows、macOS 与 Linux 桌面应用在窗口中显示管理页面；有系统托盘时关闭窗口后继续运行，Linux 桌面环境没有托盘宿主时关闭窗口即退出
- **在线更新**: 管理端“版本”页读取 `aueron1618/AIStudio2API` 的发行版本，不会更新回上游。桌面版与 Linux、macOS 命令行版可下载、校验并重启；Linux AppImage 原地替换，deb 通过系统管理员认证安装。源码开发版重新构建，容器随镜像更新
- **模块化架构**: Go 负责协议、调度、API 与管理端，SpiderMonkey 或 Camoufox 执行 WAA，Camoufox 按需处理浏览器登录

## 系统要求

- **Windows Release 运行**: Windows 10 或更高版本；系统没有 WebView2 运行时时，桌面版询问是否从微软下载安装，安装进度显示在微软安装程序的窗口中，安装完成后打开桌面窗口；取消或安装失败时在默认浏览器中打开管理页面
- **macOS 桌面版**: macOS 13 或更高版本，Apple 芯片与 Intel 通用
- **Linux 桌面版**: 基于 Ubuntu 22.04 构建，使用 GTK 3 与 WebKit2GTK 4.1，例如 Ubuntu 22.04、Debian 12 或更新版本；deb 安装这些依赖与 Camoufox 运行库，AppImage 另需下一行的 Camoufox 运行库
- **Linux Release 运行**: 解压 `aistudio2api-<版本>-linux-<amd64或arm64>.tar.gz`，进入同名目录后运行 `./aistudio2api`；选择 Camoufox 时需要 Firefox 系运行库与 Xvfb，Debian/Ubuntu 执行 `sudo apt-get update && sudo apt-get satisfy -y "libgtk-3-0, libasound2t64 | libasound2, libx11-xcb1, xvfb"`
- **源码运行**: Go 1.27+，推荐 Node.js 24.15 与 npm 11；启用 CGO，Windows 使用 [llvm-mingw](https://github.com/mstorsjo/llvm-mingw/releases) 的 UCRT clang，macOS 使用 Xcode Command Line Tools，Linux 使用 [Zig](https://ziglang.org/download/) 0.16；当前平台的 SpiderMonkey 与 libwebp 静态库分别从 [Mag1cFall/firefox](https://github.com/Mag1cFall/firefox/releases) 与 [Mag1cFall/libwebp](https://github.com/Mag1cFall/libwebp/releases) 的 Releases 下载
- **操作系统**: Windows、macOS、Linux
- **内存**: 默认进程内 SpiderMonkey、零常驻 Worker；首次请求和大上下文仍需要额外内存。原生 JS 堆不由 Go 的 `GOMEMLIMIT` 直接约束，需观察进程 RSS。配置取舍见 [运行时内存](docs/memory.md)
- **网络**: 稳定的互联网连接访问 Google AI Studio

## 安装步骤

### 方式一：Windows 一键启动（推荐）

从 [本 fork 的 Releases](https://github.com/aueron1618/AIStudio2API/releases) 下载 `aistudio2api-<版本>-windows-amd64.zip`，解压后进入同名目录，双击 `aistudio2api.exe`：

- 管理页面在桌面窗口中打开；先显示启动画面，管理服务就绪后进入面板。默认 SpiderMonkey 后端不会为启动面板下载或常驻浏览器
- 关闭窗口后程序在系统托盘继续运行：双击托盘图标显示窗口，右键打开托盘菜单；菜单可以显示窗口、在浏览器中打开管理页面、开关开机自启动或退出
- 再次双击 `aistudio2api.exe` 唤回已运行的窗口

从源码启动时：

```powershell
git clone https://github.com/aueron1618/AIStudio2API.git
cd AIStudio2API
copy .env.example .env
cd web
npm ci
npm run build
cd ..
mkdir spidermonkey
curl.exe -L -o spidermonkey\windows.tar.gz https://github.com/Mag1cFall/firefox/releases/download/v158.0.1-aistudio2api.1/spidermonkey-v158.0.1-aistudio2api.1-windows-amd64.tar.gz
tar -xzf spidermonkey\windows.tar.gz -C spidermonkey
mkdir libwebp
curl.exe -L -o libwebp\libwebp.tar.gz https://github.com/Mag1cFall/libwebp/releases/download/v1.6.0-aistudio2api.1/libwebp-v1.6.0-aistudio2api.1-windows-amd64.tar.gz
tar -xzf libwebp\libwebp.tar.gz -C libwebp
$env:CC = "x86_64-w64-mingw32-clang"
$env:CXX = "x86_64-w64-mingw32-clang++"
$env:CGO_ENABLED = "1"
$env:CGO_CPPFLAGS = "-I$PWD\spidermonkey\include -I$PWD\libwebp\include"
$env:CGO_LDFLAGS = "-L$PWD\spidermonkey\lib -L$PWD\libwebp\lib"
go build -tags desktop,production -ldflags="-H windowsgui" -o aistudio2api.exe ./cmd/aistudio2api
.\aistudio2api.exe
```

`x86_64-w64-mingw32-clang` 来自 llvm-mingw 的 `ucrt-x86_64` 包，解压后把其中的 `bin` 目录加入 `PATH`。

`start.bat` 仅启动同目录已构建的 `aistudio2api.exe`，等待退出并原样转发参数，不安装依赖或构建。开发环境与隔离数据目录见 [Windows 开发服务器](docs/development.md#windows-开发服务器)；旧的本地脚本若强制 `CGO_ENABLED=0`，需先调整原生编译环境。

`WAA_BACKEND=camoufox` 时，程序在管理服务启动后于后台准备当前平台的 Camoufox 到 `runtime/camoufox/`，生成服务在准备完成后就绪；下载期间管理页面显示进度，等待该浏览器的公开 API 请求返回说明下载进度的 503。默认 SpiderMonkey 后端仅在显式使用浏览器登录或浏览器 Cookie 验证时准备 Camoufox；HTTP Cookie 验证不需要浏览器：

- 下载经 `PROXY` 的第一个代理发出，未设置时使用系统代理
- 下载中断或连续 30 秒收不到数据时，管理页面显示原因并可重试，重试从已下载的部分继续
- 环境变量 `CAMOUFOX_PATH` 可以指定已有的同版本可执行文件，版本不符时管理页面给出所需版本

### 方式二：macOS 与 Linux 桌面版

macOS 从 Releases 下载 `darwin-universal.dmg`，打开后把 `AIStudio2API.app` 拖入“应用程序”。应用使用 ad-hoc 签名，首次打开被系统拦截时，在“系统设置 → 隐私与安全性”中点击“仍要打开”，或执行：

```bash
xattr -dr com.apple.quarantine /Applications/AIStudio2API.app
```

macOS 版关闭窗口后在菜单栏继续运行：单击菜单栏图标显示窗口，右键打开菜单，点 Dock 图标或从“应用程序”再次打开时显示窗口。

Linux 下载 `linux-amd64.AppImage` 赋予执行权限后运行，或安装 `linux-amd64.deb` 后从应用菜单启动 AIStudio2API：

```bash
chmod +x aistudio2api-<版本>-linux-amd64.AppImage
./aistudio2api-<版本>-linux-amd64.AppImage
sudo apt install ./aistudio2api-<版本>-linux-amd64.deb
```

桌面版的 `.env`、`auth/` 与 `runtime/` 位置：

- macOS：`~/Library/Application Support/AIStudio2API/`
- Linux：`~/.config/AIStudio2API/`

`setup` 等命令在终端中运行桌面版的可执行文件，例如 `/Applications/AIStudio2API.app/Contents/MacOS/aistudio2api setup --login`；deb 安装的可执行文件为 `aistudio2api`。

### 方式三：Linux 与 macOS 源码构建

#### 1. 安装依赖

- Go 1.27 或更高版本
- 推荐 Node.js 24.15 与 npm 11；也支持 Node.js 22.18+、24.11+ 或 26+
- C/C++ 编译器：macOS 为 Xcode Command Line Tools，Linux 为 [Zig](https://ziglang.org/download/) 0.16

#### 2. 克隆项目

```bash
git clone https://github.com/aueron1618/AIStudio2API.git
cd AIStudio2API
cp .env.example .env
```

#### 3. 构建并运行

```bash
cd web
npm ci
npm run build
cd ..
arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
platform=$([ "$(uname -s)" = Darwin ] && echo darwin || echo linux)
mkdir -p spidermonkey
curl -L https://github.com/Mag1cFall/firefox/releases/download/v158.0.1-aistudio2api.1/spidermonkey-v158.0.1-aistudio2api.1-$platform-$arch.tar.gz | tar -xz -C spidermonkey
mkdir -p libwebp
curl -L https://github.com/Mag1cFall/libwebp/releases/download/v1.6.0-aistudio2api.1/libwebp-v1.6.0-aistudio2api.1-$platform-$arch.tar.gz | tar -xz -C libwebp
export CGO_ENABLED=1 CGO_CPPFLAGS="-I$PWD/spidermonkey/include -I$PWD/libwebp/include" CGO_LDFLAGS="-L$PWD/spidermonkey/lib -L$PWD/libwebp/lib"
if [ "$platform" = darwin ]; then
  go build -o aistudio2api ./cmd/aistudio2api
else
  target=$(uname -m)-linux-musl
  CC="zig cc -target $target" CXX="zig c++ -target $target" go build -tags netgo,osusergo -ldflags="-linkmode external -extldflags -static" -o aistudio2api ./cmd/aistudio2api
fi
./aistudio2api
```

Linux 与 macOS 默认同样使用进程内 SpiderMonkey。源码构建需要 CGO、C/C++ 编译器和匹配的静态库，不支持 `CGO_ENABLED=0`；浏览器登录、浏览器 Cookie 验证或启用 Camoufox 后端时才准备浏览器，HTTP Cookie 验证不需要浏览器。

### 方式四：Docker

```bash
mkdir aistudio2api && cd aistudio2api
curl -fsSLO https://raw.githubusercontent.com/aueron1618/AIStudio2API/main/docker-compose.yml
mkdir data
# 将下面的占位值替换为两个不同的强密钥
cat > .env <<'EOF'
PROXY_API_KEY=<API key>
ADMIN_USERNAME=admin
ADMIN_PASSWORD=<管理密码>
EOF
chmod 600 .env
docker compose up -d
```

镜像支持 linux/amd64 与 linux/arm64，服务在容器内监听 `0.0.0.0:2048`。Compose 显式开启管理登录，并把同目录 `.env`（或宿主机环境变量）中的管理员凭据与 API 密钥传入容器；未设置 `ADMIN_PASSWORD` 或 `PROXY_API_KEY` 时拒绝启动。这里的 `.env` 用于 Compose 变量替换，不会自动整份传入容器。

提供两种镜像，均默认使用进程内 SpiderMonkey 和零热池：

| 版本 | 滚动标签 | main 提交标签 | 固定版本标签 |
| --- | --- | --- | --- |
| 内置浏览器（默认） | `latest` | `main-<6位SHA>` | `vX.Y.Z`、`X.Y.Z` |
| 无浏览器 | `latest-no-browser` | `main-<6位SHA>-no-browser` | `vX.Y.Z-no-browser`、`X.Y.Z-no-browser` |

内置浏览器版在构建时打包对应架构、版本匹配的 Camoufox，以及 Windows 字体、fontconfig、Xvfb 和软件 GL 运行库；移除未使用的 macOS 字体与 fontconfig。路径为 `/opt/camoufox/camoufox-bin`，通过镜像内 `CAMOUFOX_PATH` 指定；浏览器 Cookie 验证、登录及 Camoufox 后端无需运行时再下载，也不会因预装浏览器而常驻浏览器进程。该版本构建需要访问 GitHub Releases，镜像包含完整浏览器体积。

无浏览器版仅保留应用（含静态链接的 SpiderMonkey 与 libwebp）、系统证书与时区数据，不包含 Camoufox、配套字体或 GTK/Xvfb/GL 依赖，构建时也不下载浏览器。请保持 `WAA_BACKEND=spidermonkey`，通过 Cookie **HTTP 验证**、已有登录态文件或远程添加导入账户。浏览器登录、默认 Cookie 浏览器验证及 `WAA_BACKEND=camoufox` 需要内置浏览器版；无浏览器版没有改变应用的按需下载机制，不应在该版本触发这些浏览器功能。

CI 在本仓库 `main` 与 `v*` tag 推送时，为两种版本分别构建并发布 `ghcr.io/aueron1618/aistudio2api` 的双架构镜像。`main` 推送会更新 `latest` / `latest-no-browser` 并生成对应的 `main-<6位SHA>` 标签；正式版本标签也更新 latest，预发布标签不覆盖 latest。当前工作流不生成裸 `main` 或 `main-no-browser` 标签，latest 不代表仅稳定发行版；需固定版本时使用版本标签或 digest。CI 复用同次构建的 Linux 静态发行程序，不重复编译前端与 Go；每个变体、架构均执行断网只读冒烟，每个变体收齐两个架构 digest 后才发布 manifest。HTML 预览另使用 `2050` 端口。

使用无浏览器版时，将已有 Compose 服务的 `image` 和 `build` 字段改为以下内容，其余配置保持不变；`build.target` 确保使用 `--build` 时不会构建成内置浏览器版：

```yaml
image: ghcr.io/aueron1618/aistudio2api:latest-no-browser
build:
  context: .
  target: no-browser
```

从源码分别构建两种版本：

```bash
docker build --target browser -t aistudio2api:browser .
docker build --target no-browser -t aistudio2api:no-browser .
```

不指定 `--target` 时仍构建内置浏览器版。

`data/` 保存程序配置 `.env`、`auth/` 与 `runtime/`；内置浏览器版的浏览器位于 `/data` 挂载之外，更新镜像即可更新配套浏览器，不需要迁移旧的 `data/runtime/camoufox/`。迁移已有安装时把这三项复制到 `data/`，并将管理员凭据与 API 密钥配置到 Compose 同目录的 `.env`。容器环境变量通常优先于 `data/.env`，但 API 密钥是例外：在面板修改 `PROXY_API_KEY` 会热更新认证，并在 `data/.env` 写入 `PROXY_API_KEY_WEB_OVERRIDE=true`，重启后仍优先使用保存的密钥，不再被 Compose 注入的旧值覆盖；恢复环境变量优先需移除此标记并重启。管理员凭据等其他已注入配置仍由 Compose 决定。容器以 root 运行，服务写入 `data/` 的文件属于 root，Linux 宿主机上用 `sudo` 编辑。

- 打开 `http://服务器IP:2048/login`，使用管理员账号密码登录后访问面板，用户名默认为 `admin`；`PROXY_API_KEY` 独立用于生成 API。公网部署应使用 HTTPS 反向代理，并保留原始 `Host`、设置 `X-Forwarded-Proto: https`
- 添加账户：把能登录 Google 的电脑上的 `auth/<Google 邮箱>/` 复制到 `data/auth/`；或在管理页面账户页点击“远程添加”取得配对令牌，经 SSH 隧道 `ssh -L 2048:127.0.0.1:2048 <服务器>` 后在该电脑运行 `setup --remote http://127.0.0.1:2048`
- 添加账户：内置浏览器版可在“添加账户”中选择“浏览器登录”，在管理页面打开的登录窗口中登录 Google；两种版本均可粘贴 Cookie 并选择 HTTP 验证，或把能登录 Google 的电脑上的 `auth/<Google 邮箱>/` 复制到 `data/auth/`
- `PROXY` 与账户代理从容器内访问，代理运行在宿主机时填写宿主机的局域网地址，并允许代理接受局域网连接
- 容器默认使用 UTC，不固定 `TZ`。未显式指定的账户时区、服务日志与按日账本统计使用服务器时区；需要其他时区时，在已有 `environment` 下追加 `TZ: <IANA 时区>`，不要替换管理认证与 API 密钥条目。账户时区也可单独设置
- 修改 Compose 同目录的 `.env` 或 `docker-compose.yml` 后执行 `docker compose up -d --force-recreate`，仅 `restart` 不会更新容器环境变量；修改 `data/.env` 中未被环境变量覆盖的配置后可执行 `docker compose restart`。更新镜像执行 `docker compose pull && docker compose up -d`；在源码目录中执行 `docker compose up -d --build` 从源码构建

## 快速开始

### 首次使用（需要认证）

1. **准备首个账户**:

   Windows 可以导入本机 Chrome 账户：

   ```powershell
   cmd /c start /wait aistudio2api.exe setup
   ```

   Linux 与 macOS 使用隔离 Camoufox 登录：

   ```bash
   ./aistudio2api setup --login
   ```

   登录完成后会从 AI Studio 页面读取 Google 邮箱，并为账户授权 Google Drive。账户保存到 `.env` 中 `AISTUDIO_AUTH_STATES` 指向的目录；语言和时区默认读取当前电脑设置，也可以通过 `--locale`、`--timezone` 指定。

2. **启动图形界面**:
   - Windows 双击 `aistudio2api.exe`
   - macOS 与 Linux 桌面版从应用程序启动，命令行版运行 `./aistudio2api`
   - 桌面版在窗口中打开管理页面，命令行版在浏览器中打开 `http://127.0.0.1:2048`
   - 未开启自动启动时，页面初始状态为 `STOPPED`；默认显示“日志”页面，添加账户时切换到“账户”

3. **添加其他账户**:
   - 打开“账户”页面，点击“添加账户”选择方式
    - “从 Chrome 导入”可多选本机 Chrome 账户；受验证的同邮箱账户可原地重导入，保留已有账户配置与资源归属。原始 Cookie/CDP 导入不覆盖已有账户
   - “粘贴 Cookie”支持粘贴或读文件：Cookie 请求头、多行文本、Cookie-Editor JSON 数组、含 `cookies` 的 storage-state JSON、Netscape 文件；每项 UTF-8 最大 1 MiB、最多 1000 条 Cookie。需包含有效的 `SAPISID`、`__Secure-1PAPISID`、`__Secure-3PAPISID`。请从 Network 复制完整 Cookie，`document.cookie` 会遗漏 HttpOnly。默认在无头浏览器中验证，也可显式选择 HTTP 验证；失败不会自动切换验证方式。成功后识别真实邮箱并保存轮换 Cookie，已有账户不会被覆盖
   - 管理接口 `POST /api/accounts/import/cookies` 使用 `{items:[{email?,cookies?,cdp?,auth_user,method?}],proxy?,locale?,timezone?}`，每批最多 100 项、JSON 上限 8 MiB。`method` 在每一项内，省略时为 `browser`，`http` 必须显式选择；`auth_user` 为 `/u/N/` 的会话序号 0 到 9，省略时为 0。上传 `email` 不是可信身份，受管理登录与同源保护
   - 创建、重新登录、Chrome 与 Cookie 导入均返回 `202 {task}`，不是同步导入成功；通过 `GET /api/accounts/tasks` 或 SSE `account_tasks` 查看各项结果。明显非法的 `method` / `auth_user` 在接收任务前返回 400；解析、验证与重复账户失败写入 `task.items[].code`。不再提供旧的 201 同步单对象导入
   - HTTP 导入任务完成只表示已验证并提交认证状态；该路径不启动 WAA 或浏览器同步新 PageCatalog，模型目录由后续显式启动服务或刷新按 Worker 预算获取。目录、WAA、生成与 Drive 授权需分别确认就绪
   - Cookie 仅导入 Google 字段，不信任上传身份或 OAuth 扩展，不延长过期 Cookie，也不取得 Chrome 续签材料或交互式 Drive 授权。仅导入自己的账户，使用 HTTPS 或 SSH 隧道，勿分享或提交 Cookie 到版本库
   - “从指纹浏览器导入”自动列出本机指纹浏览器与 Chromium 中已登录 AI Studio 的窗口及其语言与时区，勾选后导入，账户沿用该浏览器的语言与时区；浏览器需开启远程调试
   - “浏览器登录”会打开独立 Camoufox 窗口，登录完成后自动识别邮箱、授权 Google Drive 并保存；Google 要求验证身份时，在该窗口或手机上确认
   - 服务器没有图形界面，或从其他电脑、经反向代理打开管理页面时，“浏览器登录”与“重新登录”的 Camoufox 在服务器上无头运行，画面显示在管理页面的全屏登录窗口中，在其中点击、输入与粘贴，点“取消”结束登录
   - 账户代理、语言区域与时区可在导入/登录/编辑对话框设置。新账户地区不预填，当前为手工输入 locale/IANA 时区，不提供候选选择；Cookie 对话框中的可选字段位于“更多设置”。保存写入账户配置，不只是界面语言

4. **启动 API**:
   - 点击“启动服务”启动数据面
   - 状态依次显示 `LAUNCHING` 和 `RUNNING`；`LAUNCHING` 期间可以点击“停止服务”取消启动
   - 在“日志”页面确认账户、模型和请求状态
   - API 默认监听 `http://127.0.0.1:2048`

账户操作随状态显示：

| 账户状态 | 可用操作 |
| --- | --- |
| `ready` | 编辑、停用、立即刷新、删除 |
| `disabled` | 编辑、启用、删除 |
| `auth_required` | 编辑、停用、重新登录、立即刷新、删除 |
| `restricted` | 编辑、停用、立即刷新以重新验证、删除 |

“重新登录”只在账户状态为 `auth_required` 时显示。

关闭或刷新页面、隐藏登录窗口不会取消已接收的任务。`DELETE /api/accounts/tasks/{id}` 取消任务并等待执行释放后返回 204，同时移除记录；停服取消但未被 DELETE 的任务保留为 `failed`，未完成项的 `code` 为 `account_task_canceled`。任务仅在管理进程内保存，进程重启不恢复。

### 日常使用（已有认证）

1. 桌面版双击 `aistudio2api.exe` 或从应用程序启动；命令行版运行 `./aistudio2api`
2. 点击“启动服务”启用 API；设置 `AUTO_START=true` 后，程序启动时自动启动服务
3. 点击“停止服务”会取消正在进行的启动或活动请求并关闭 WAA Worker，管理页面与日志保持可用
4. 再次点击“启动服务”即可恢复 API

停止后再次启动会读取最新 `.env` 生成服务配置，账户选择策略与 Worker 容量保存后立即生效；管理页面地址和预览地址在管理进程重启后生效。设置页保存 `PROXY_API_KEY` 和 key 账户绑定后立即用于新请求，不需要重启管理进程或生成服务。

有系统托盘时，桌面版关闭窗口后继续运行，从托盘菜单“退出”结束管理进程；Linux 没有托盘宿主时关闭窗口即退出。命令行版在启动窗口按 `Ctrl+C` 或关闭窗口会退出整个管理进程。关闭浏览器标签页不会停止管理进程。

### 快速启动

Windows 桌面版在命令行中运行以下命令时写作 `cmd /c start /wait aistudio2api.exe <参数>`，命令行等程序结束后返回。

`aistudio2api`：启动管理进程并打开管理页面，桌面版打开窗口，命令行版打开网页。

`aistudio2api -open-ui=false`：启动管理进程但不打开窗口或网页；桌面环境有系统托盘时显示托盘图标。

`aistudio2api setup`：导入账户。

- 不带参数时扫描本机 Chrome 账户，`--email` 或 `--profile` 选择明确的 Chrome 账户
- 隔离登录：`aistudio2api setup --login`，需要图形界面；没有图形界面时启动服务，在管理页面的“浏览器登录”中登录
- 文件导入：`aistudio2api setup --storage-state <file>`
- Cookie 导入：`aistudio2api setup --cookie-file <file>`、`aistudio2api setup --cookie "<Cookie 请求头>"` 或 `aistudio2api setup --cdp <调试地址>`，账户邮箱按 Cookie 识别，`--cdp` 导入的账户沿用该浏览器的语言与时区；Cookie 来自同时登录多个 Google 账户的浏览器时，以 `--authuser N` 选择官网地址 `/u/N/` 中第 N 个账户

`aistudio2api setup --remote https://<远程实例>`：向远程实例添加账户。在远程实例的账户页点击“远程添加”，对话框给出完整命令（Windows 为 `cmd /c start /wait aistudio2api.exe setup --remote <地址> --token <令牌>`，其他系统为 `./aistudio2api setup --remote <地址> --token <令牌>`），账户来源可选“该电脑 Chrome 中的账户”或“在新窗口登录”（命令带 `--login`），一键复制后在能登录 Google 的电脑上运行；省略 `--token` 时运行后从标准输入读取令牌。对话框打开期间沿用仍有效的令牌，令牌到期时自动换新，点击“换一个令牌”立即换新，旧令牌随即失效。从该电脑 Chrome 导入的账户只能由这台电脑续签：命令上传这类账户后留在前台，立即并每 100 分钟向远程实例发送它们的新会话，失败时 5 分钟后重试，远程实例已删除账户或续签凭据失效时停止为该账户续签；保持该窗口打开，重启电脑后运行 `aistudio2api setup --remote <地址> --renew` 恢复续签。

原电脑的 `runtime/remote-renewals.json` 保管 Chrome OAuth 材料与每账户续签 token；服务器验证上传 Cookie 的真实身份，只在账户目录的 `pairing-renewal.json` 保存 token 摘要。不要把这些续签文件或 token 分享给他人，服务器不能替代原电脑独立续签。

## API 使用

### OpenAI 兼容接口

服务启动后，可以直接使用 OpenAI Chat Completions：

```bash
curl http://127.0.0.1:2048/v1/chat/completions \
  -H "Authorization: Bearer 123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.7-flash",
    "messages": [{"role": "user", "content": "Hello, world!"}],
    "stream": true
  }'
```

### 客户端配置示例

管理端“接入”页可按所选模型复制客户端配置、SDK/cURL 示例和端点说明。示例遵守下述固定通道：目录标为 Build-only 的模型使用 `/build/v1`，不能把所有客户端硬编码到 `/v1`；接口中列出的工具或媒体仍需实时目录声明支持。

| 协议 | Base URL | API key |
| --- | --- | --- |
| OpenAI Chat / Responses | `http://127.0.0.1:2048/v1` | `.env` 中的 `PROXY_API_KEY` |
| Anthropic Messages | `http://127.0.0.1:2048` | `.env` 中的 `PROXY_API_KEY` |
| Gemini | `http://127.0.0.1:2048` | `.env` 中的 `PROXY_API_KEY` |

OpenAI / Anthropic 的 `/v1` 固定使用 **Playground** 通道；使用 **Build** 时将 Base URL 改为 `http://127.0.0.1:2048/build/v1`（Anthropic 的服务器地址改为 `http://127.0.0.1:2048/build`）。模型列表和单模型查询同样按通道过滤。上游已移除 `-build` 写代码助手模型及 `assistant` 通道，旧 Build 应用代理仍保留，二者不是同一个通道。固定入口不会因限额、结构化输出、模型映射或非流式优先选项切换通道；目标通道需要在 `UPSTREAM_CHANNELS` 中启用。Build 不支持的专用能力不会改走 Playground。`/v1beta` 保留自动路由。

模型名称从对应通道的模型接口或 `GET /v1beta/models` 读取。`PROXY_API_KEY` 为空时，浏览器中只有本机页面可以直接调用接口；网页版客户端和部分桌面客户端需要设置密钥。多个 key 用逗号分隔，客户端只填写其中一个；设置页可为每个 key 勾选处理其请求的账户，未配置绑定的 key 可用全部账户。请求带 `X-AIStudio-Account: <账户>` 请求头时只由该账户处理，账户填账户页显示的邮箱，仍须满足该 key 的账户范围；该账户出错时直接返回它的错误，不换到其他账户。

以 Cherry Studio 为例：

1. 打开 Cherry Studio 设置
2. 新增 OpenAI 兼容提供商
3. API 主机地址填写 `http://127.0.0.1:2048/v1`
4. API 密钥填写 `.env` 中的 `PROXY_API_KEY`
5. 从 `/v1/models` 获取模型，或手动添加 `gemini-3.6-flash`、`gemini-3.7-flash`

[Claude Code](https://github.com/anthropics/claude-code) 使用 Anthropic 接口，子 Agent 按 opus、sonnet、haiku 档位选择模型，以下变量把它们映射到 AI Studio 模型；WebSearch 由 Google Search 执行：

```powershell
$env:ANTHROPIC_BASE_URL = "http://127.0.0.1:2048"
$env:ANTHROPIC_API_KEY = "<PROXY_API_KEY>"
$env:ANTHROPIC_MODEL = "gemini-3.8-flash"
$env:ANTHROPIC_DEFAULT_OPUS_MODEL = "gemini-3.1-pro-preview"
$env:ANTHROPIC_DEFAULT_SONNET_MODEL = "gemini-3.8-flash"
$env:ANTHROPIC_DEFAULT_HAIKU_MODEL = "gemini-3.5-flash-lite"
```

[Codex](https://github.com/openai/codex) 使用 Responses 接口，在 `~/.codex/config.toml` 中添加 provider，并把 `PROXY_API_KEY` 写入 `AISTUDIO2API_KEY` 环境变量；Codex 的 `web_search` 工具由 Google Search 执行：

```toml
model = "gemini-3.8-flash"
model_provider = "aistudio"

[model_providers.aistudio]
name = "AIStudio2API"
base_url = "http://127.0.0.1:2048/v1"
env_key = "AISTUDIO2API_KEY"
wire_api = "responses"
```

[omp](https://github.com/can1357/oh-my-pi) 的 `web_search` 工具使用 `web` 角色的模型搜索。在 omp 的 `models.yml` 中添加指向本服务的 Gemini provider（`apiKey` 为保存 `PROXY_API_KEY` 的环境变量名），并在 `config.yml` 中把 `web` 角色设为该模型：

```yaml
# models.yml
providers:
  aistudio-gemini:
    baseUrl: http://127.0.0.1:2048/v1beta
    api: google-generative-ai
    apiKey: AISTUDIO2API_KEY
    models:
      - id: gemini-3.8-flash
```

```yaml
# config.yml
modelRoles:
  web: aistudio-gemini/gemini-3.8-flash
```

主要端点：

| 能力 | 端点 |
| --- | --- |
| 模型 | `GET /v1/models`、`GET /v1/models/{model}`、`GET /v1beta/models`、`GET /v1beta/models/{model}` |
| OpenAI Chat | `POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses`、`POST /v1/responses/input_tokens`、`POST /v1/responses/compact`、`GET`/`DELETE /v1/responses/{id}`、`GET /v1/responses/{id}/input_items`、`POST /v1/responses/{id}/cancel` |
| Files | `POST /v1/files`、`GET /v1/files`、`GET /v1/files/{id}`、`GET /v1/files/{id}/content`、`DELETE /v1/files/{id}`（OpenAI 与 Anthropic 格式）；`POST /upload/v1beta/files`、`GET /v1beta/files`、`GET`/`DELETE /v1beta/files/{id}` |
| Anthropic | `POST /v1/messages`、`POST /v1/messages/count_tokens` |
| Gemini | `POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent`、`:countTokens`；`POST`/`GET /v1beta/cachedContents`、`GET`/`PATCH`/`DELETE /v1beta/cachedContents/{id}` |
| Gemini Decision | `POST /v1beta/decisions` |
| Embeddings（Build） | `POST /build/v1/embeddings`、`POST /v1beta/models/{model}:embedContent`、`:batchEmbedContents` |
| 图片 | `POST /v1/images/generations`、`POST /v1/images/edits`、Imagen `POST /v1beta/models/{model}:predict` |
| 语音 | `POST /v1/audio/speech` |
| 转录与翻译 | `POST /v1/audio/transcriptions`、`POST /v1/audio/translations` |
| 音乐 | Gemini `generateContent` + `responseModalities: ["AUDIO"]` |
| 视频 | `POST /v1/videos`、`GET /v1/videos`、`GET`/`DELETE /v1/videos/{id}`、`GET /v1/videos/{id}/content` |
| Gemini 视频 | `POST /v1beta/models/{model}:predictLongRunning`、`GET /v1beta/operations/{id}` |
| Live（含实时翻译、实时转录与实时音乐）/ Robotics | `GET /v1/live`、`GET /v1/robotics/stream` |

四套生成接口均可按各自协议字段启用 Search、Image Search、URL Context、Code Execution 和 Maps。Files、Transcribe、Live、Robotics 的请求与事件格式见 [Google AI Studio 协议规范](docs/protocol.md)。

生成请求中的内联附件会优先上传到生成账户的 Drive，之后带相同附件的请求优先发往已上传该附件的账户并直接引用已上传的文件，闲置 1 小时后删除；账户未授予 Drive 权限或存储配额不足时保持内联数据发送。图片、音频、视频、PDF 等输入仍需所选模型支持。重复使用的附件可通过 Files 接口上传一次并复用文件 ID。

Gemini 附件与视频图片输入支持 `inlineData` / `inline_data`、`fileData` / `file_data`、`mimeType` / `mime_type` 和 `fileUri` / `file_uri`。媒体 Base64 数据支持标准与 URL-safe 字母表、带填充与无填充形式，以及 `data:<MIME>;base64,` 前缀。OpenAI 助手历史中的 Markdown 图片同样支持 URL-safe Base64 和 CR/LF 换行。内联 GIF 和视频表单上传的 GIF 按首帧静态图片转换为 PNG，保留逻辑画布、帧位置与透明背景；经 Files API 上传的 GIF 按首帧转换为白底 JPEG。

### TTS 语音生成

TalkifyTTS 与新版 Google Gen AI SDK 可连接 `http://127.0.0.1:2048/v1beta/interactions`，稳定版入口为 `/v1/interactions`。请求使用 `x-goog-api-key`，支持 `gemini-3.8-flash-tts` 与 `gemini-3.8-flash-lite-tts`：

```python
from google import genai

client = genai.Client(api_key="123", http_options={"base_url": "http://127.0.0.1:2048"})
stream = client.interactions.create(
    model="gemini-3.8-flash-tts",
    input="Hello, this is a test.",
    response_format={"type": "audio"},
    generation_config={"speech_config": [{"voice": "Kore"}]},
    stream=True,
)
for event in stream:
    if event.event_type == "step.delta" and event.delta.type == "audio":
        print(event.delta.data)
```

流式音频默认为 Base64 编码的 24 kHz、16-bit 小端、单声道 PCM；非流式默认为完整 WAV，通过 `interaction.output_audio.data` 读取。`response_format.mime_type` 可显式选择 `audio/l16` 或 `audio/wav`。风格与多说话人输入见 [Interactions 协议](docs/protocol.md#gemini-interactions)。

OpenAI `POST /v1/audio/speech` 省略 `response_format` 时默认输出 MP3；下例显式请求 WAV，输出文件扩展名与格式一致。

```bash
curl http://127.0.0.1:2048/v1/audio/speech \
  -H "Authorization: Bearer 123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.1-flash-tts-preview",
    "input": "Hello, this is a test.",
    "voice": "Kore",
    "response_format": "wav"
  }' \
  --output speech.wav
```

多说话人语音可以通过 Gemini `generateContent` 的 `multiSpeakerVoiceConfig` 配置。

```bash
curl http://127.0.0.1:2048/v1beta/models/gemini-2.5-flash-preview-tts:generateContent \
  -H "x-goog-api-key: 123" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [{"parts": [{"text": "Joe: How are you?\nJane: I am fine, thanks!"}]}],
    "generationConfig": {
      "responseModalities": ["AUDIO"],
      "speechConfig": {
        "multiSpeakerVoiceConfig": {
          "speakerVoiceConfigs": [
            {"speaker": "Joe", "voiceConfig": {"prebuiltVoiceConfig": {"voiceName": "Kore"}}},
            {"speaker": "Jane", "voiceConfig": {"prebuiltVoiceConfig": {"voiceName": "Puck"}}}
          ]
        }
      }
    }
  }' --output speech.json
```

可用语音由实时模型目录中的 `capability_options.voices` 返回。`gemini-3.8-flash-tts` 等带 `speech_metadata` 能力的模型同样接受上面的 `说话人: 台词` 写法，也可以为每个文本 part 设置 `speechMetadata.speaker` 与 `speechMetadata.style`，并用 `multiSpeakerVoiceConfig.mode` 选择 `VERBATIM` 或 `CONVERSATIONAL`；OpenAI `instructions` 在这些模型上作为语音风格。

### 图片生成 (Nano Banana)

```bash
curl http://127.0.0.1:2048/v1/images/generations \
  -H "Authorization: Bearer 123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.1-flash-image",
    "prompt": "A cute cat wearing a tiny hat",
    "n": 1,
    "size": "1024x1024"
  }'
```

`size` 为输出图片的宽高，例如 `2560x1440`，服务按最接近的宽高比与能覆盖该尺寸的分辨率生成后缩放为该尺寸；`size` 为 `auto` 或省略时，`quality` 的 `low`、`medium`、`high` 分别按 1K、2K、4K 生成。`output_format` 取 `png`（默认）、`jpeg` 或 `webp`。`/v1/images/edits` 与 Responses 的 `image_generation` 工具使用同样的字段，Gemini 原生接口使用 `imageConfig.aspectRatio` 与 `imageConfig.imageSize`。

### 视频生成

```bash
curl http://127.0.0.1:2048/v1/videos \
  -H "Authorization: Bearer 123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-omni-1.1-flash",
    "prompt": "A drone flying over a forest"
  }'
```

创建任务后通过 `GET /v1/videos/{id}` 查询状态，通过 `GET /v1/videos/{id}/content` 下载结果；`gemini-omni-1.1-flash` 等能输出视频的 Omni 模型同样经 `:predictLongRunning` 创建任务。

Veo 专用实现已移除。升级前尚未完成的 Veo 任务不能继续执行或恢复；已绑定产物文件的记录仍可读取，前提是文件和所属账户仍可用。视频端点保留，但模型须具备当前 Omni 视频输出能力。

### 决策 (Gemini Decision)

```bash
curl http://127.0.0.1:2048/v1beta/decisions \
  -H "x-goog-api-key: 123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-decision-1",
    "input": "I was charged twice for my order.",
    "questions": {
      "is_refund": {"type": "noul", "instructions": "Is the customer asking for money back?"},
      "department": {
        "type": "choice",
        "instructions": "Which department should handle this request?",
        "criteria": {"billing": "Payments and refunds.", "shipping": "Delivery and tracking.", "other": ""}
      }
    }
  }'
```

`questions` 的键由调用方命名，回答按同样的键返回。`noul` 返回为真的概率 `noul`，`criteria` 可写 `true_description` 与 `false_description`；`choice` 的 `criteria` 为选项 ID 到说明，返回 `choice` 与各选项的 `probabilities`；`score` 的 `criteria` 为从低到高的等级说明，返回期望等级 `score`、各等级的 `probabilities` 与 `legend`。每个回答都带 `confidence`。`input` 接受字符串或 Interactions 内容块，图片、音频、视频与 PDF 以 `data`（Base64）或 `uri`（HTTPS 链接或本服务 Files API 的文件）给出并写明 `mime_type`。Google Gen AI SDK 以 `client.decisions.create` 调用。

AI Studio 官网前端尚未调用 `gemini-decision-1`：官网 Playground 选这个模型时只列出示例应用，示例应用经 Gemini API 以付费 API key 调用；本服务经网页的 CreateDecision 请求调用，官网页面不会发出这类请求，每次调用在运行日志中提醒。

## 模型

模型目录会随 AI Studio 更新，客户端从 `/v1/models` 或 `/v1beta/models` 读取当前值。下表为目录中的常用模型，模型 ID 与上限以运行时结果为准：

| Model ID | 名称 | 类型 | 输入 | 输出 |
| --- | --- | --- | ---: | ---: |
| `gemini-4-carbon` | Gemini 4 Carbon | 文本 | 1048576 | 32768 |
| `gemini-4-argon` | Gemini 4 Argon | 文本 | 1048576 | 32768 |
| `gemini-3.8-flash` | Gemini 3.8 Flash | 文本 | 1048576 | 65536 |
| `gemini-3.6-flash` | Gemini 3.6 Flash | 文本 | 1048576 | 65536 |
| `gemini-3.5-flash-lite` | Gemini 3.5 Flash Lite | 文本 | 1048576 | 65536 |
| `gemini-3.1-pro-preview` | Gemini 3.1 Pro Preview | 文本 | 1048576 | 65536 |
| `gemini-3.1-flash-lite` | Gemini 3.1 Flash Lite | 文本 | 1048576 | 65536 |
| `gemma-4-31b-it` | Gemma 4 31B IT | 文本 | 262144 | 32768 |
| `gemini-omni-1.1-flash` | Gemini Omni 1.1 Flash | 文本与视频 | 131072 | 65536 |
| `deep-research-preview-04-2026` | Deep Research Preview | 深度研究 | 131072 | 65536 |
| `gemini-decision-1` | Gemini Decision 1 | 决策 | 262144 | 0 |
| `gemini-nano-banana-2.1` | Nano Banana 2.1 | 图片 | 65536 | 65536 |
| `gemini-3.1-flash-image` | Nano Banana 2 | 图片 | 65536 | 65536 |
| `gemini-3.1-flash-lite-image` | Nano Banana 2 Lite | 图片 | 65536 | 65536 |
| `gemini-3-pro-image` | Nano Banana Pro | 图片 | 131072 | 32768 |
| `gemini-3.8-flash-tts` | Gemini 3.8 Flash TTS | 语音合成 | 8192 | 16384 |
| `gemini-3.8-flash-lite-tts` | Gemini 3.8 Flash Lite TTS | 语音合成 | 8192 | 16384 |
| `gemini-3.5-transcribe` | Gemini 3.5 Transcribe | 转录 | 98304 | 32768 |
| `lyria-3.5` | Lyria 3.5 | 音乐 | 1048576 | 65536 |
| `lyria-3-pro-preview` | Lyria 3 Pro Preview | 音乐 | 1048576 | 65536 |
| `gemini-3.8-live` | Gemini 3.8 Live | 实时对话 | 131072 | 65536 |
| `gemini-3.5-live-translate-preview` | Gemini 3.5 Live Translate Preview | 实时翻译 | 16384 | 32768 |

目录中另有 `-latest` 别名、Robotics-ER 与早期版本的图片与 TTS 模型。

公开端点实现标准 `generateContent`、`countTokens`、`embedContent`、`batchEmbedContents`、`predictLongRunning` 和 `createDecision`（`/v1beta/decisions`）；`bidiGenerateContent` 与 `bidiGenerateMusic` 模型经 `/v1/live` 调用。公开模型接口汇总各账户实时上游目录，按入口过滤通道，再投影本地映射、搜索与可选抗截断别名；管理目录保留真实上游模型。调度按目录明确提供的模型 ID、方法、能力字段和账户当前运行状态选择账户。

启用账户的订阅都不满足的模型（如只对 Google AI Ultra 开放的模型）同样列在目录中、不含 `channels`，调用时返回 403 并写明所需订阅。启用 `AUTO_CONTINUATION` 时，能力含 `output_continuation` 的模型（如 Gemini 4 Argon）与官网一样接受到 `input_token_limit` 的输出上限，未指定输出上限时取 `input_token_limit`，超过 32768 时分轮自动续写，各轮输出合并为一个响应；关闭时采用单轮上限，不再发起下一轮。模型名可以带思考后缀，例如 `gemini-4-argon(low)`：括号内为 `none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`extra_high`、`max`、`auto` 或思考预算 token 数，后缀优先于请求体中的思考参数。客户端写死的模型名可用 `MODEL_MAPPING` 映射到目录中的模型，例如 `MODEL_MAPPING=claude-haiku-5-5:gemini-3.8-flash`，别名以目标模型的字段列在模型目录中。

`ANTI_TRUNCATION=true` 时，公开目录同时列出普通模型和 `抗截断-<模型>` 变体，保留模型映射、思考后缀与搜索别名的语义。通用文本生成使用内部函数承载正文，不扩大模型能力；实际走上游 Interaction/Agent 执行路径的模型仍不支持这种函数包装，这不等于公开 Interactions 协议整体不可用。

计数、Embedding、Imagen、图片、语音、转录/翻译、视频任务、Live/Robotics 和显式缓存入口会校验并将此前缀解析为基础模型别名，保持各自原生的向量、媒体或结构化格式，不强制套用文本函数。关闭开关后不再列出变体，所有这些入口均拒绝带此前缀的新请求，普通模型仍按原方式处理。

抗截断的 normal view 将内部传输函数还原为正文，公开响应的 `model` / `modelVersion` 使用基础模型名，不泄漏内部工具名；这只是响应与展示层的处理，不改写客户端请求、真实目录或审计中的 `requested_model`。用量界面仅移除一次保留前缀，并保留 `models/` 与模型后缀。工具统计按实际观察到的调用累计，可为 0 或多次，不因开启抗截断而固定填 1。

## 项目架构

```text
AIStudio2API/
├── cmd/aistudio2api/        # 程序入口与 Windows 资源
├── internal/
│   ├── app/                 # 命令、管理监听、生成服务生命周期与调度
│   ├── config/              # .env 配置项的读取与校验
│   ├── setup/               # 账户导入和独立登录 CLI
│   ├── aistudio/            # AI Studio 协议、认证、模型与媒体
│   ├── api/                 # OpenAI、Responses、Anthropic 与 Gemini 适配
│   ├── imagecodec/          # JPEG 解码、CatmullRom 缩放、PNG 与 libwebp WebP 编码
│   ├── resample/            # 语音 PCM 的 Kaiser 窗 sinc 多相重采样
│   ├── waa/                 # SpiderMonkey WAA 运行时
│   ├── camoufoxnative/      # Camoufox BiDi、登录与 WAA Worker
│   ├── chromeauth/          # Windows Chrome 与 DBSC 导入
│   ├── proxydial/           # 全局与账户代理的拨号与转发
│   ├── sqlitedb/            # SQLite 打开、表结构同步与空闲页回收
│   ├── requestdb/           # 请求用量、汇总与正文账本
│   ├── statedb/             # 已结束 Responses / Interactions 续接状态
│   ├── promptdb/            # API 试用页对话存储
│   ├── pricing/             # 用量页费用估算
│   ├── i18n/                # 服务文案的界面语言翻译
│   ├── webui/               # 内嵌前端产物
│   └── desktop/             # 桌面窗口与系统托盘
├── web/                     # Vue 3 + TypeScript 前端
│   └── src/
│       ├── playground/      # API 试用页
│       ├── components/      # 管理页面组件
│       └── locales/         # 界面语言
├── docs/                    # 构建、开发、协议、日志与 WAA 文档
├── build/                   # 应用图标与各平台打包配置
├── .github/workflows/       # 检查、五平台构建与 Release 发布
├── .env.example             # 配置模板
├── Dockerfile               # Docker 镜像
├── docker-compose.yml       # Docker Compose 示例
├── go.mod                   # Go 模块与依赖
└── LICENSE
```

运行时在程序目录生成 `.env`、`auth/`（账户认证材料）与 `runtime/`（Camoufox、本地数据库、日志与媒体文件）。

## 配置说明

### 环境变量配置

复制并编辑环境配置文件：

```bash
cp .env.example .env
```

进程环境变量通常覆盖 `.env` 中的同名项，并在设置页中锁定；`PROXY_API_KEY` 始终可在设置页修改，保存新密钥后立即生效，`.env` 中自动写入的 `PROXY_API_KEY_WEB_OVERRIDE=true` 使它在重启后仍优先于进程环境。除该覆盖标记外，管理页面保存设置时 `.env` 只写入与默认值不同的项。普通配置的无效值按默认值运行，并在管理页面顶部与日志中列出；`API_KEY_ACCOUNTS` 格式无效或重复指定同一已配置 key 时拒绝加载和保存，不回退成允许全部账户。升级前备份配置：无注释且包含旧版全量键的 `.env` 可能在读取时自动精简，并非只在点击保存时改写。

下表为未配置时的程序默认值；`.env.example` 显式选择仅 Playground，并关闭 Build 非流式优先、输入续写改写和分轮自动续写，可按需开启。

| 变量 | 默认值 | 作用 |
| --- | --- | --- |
| `AISTUDIO_AUTH_STATES` | `auth` | 账户文件、目录或多个逗号分隔路径 |
| `LISTEN_ADDR` | `127.0.0.1:2048` | 管理页面与 API 监听地址 |
| `PREVIEW_LISTEN_ADDR` | `127.0.0.1:2050` | API 试用页 HTML 预览的监听地址，端口与 `LISTEN_ADDR` 不同，无法监听时改用同一主机上的空闲端口；页面以当前访问的主机名加实际端口打开预览 |
| `PROXY_API_KEY` | 空 | 公开 API key，多个以逗号分隔；设置页保存后立即生效，无需重启 |
| `API_KEY_ACCOUNTS` | 空 | 各 key 可用的账户，每项写成 `key:账户\|账户`，未列出的 key 可用全部账户；也可在设置页按 key 勾选账户，保存后立即生效 |
| `AUTO_START` | `false` | 管理程序启动后自动启动生成服务；网络等原因启动失败时按 5 秒起、翻倍至 5 分钟的间隔重试，手动启动或停止后不再重试 |
| `HIDE_DOCK_ICON` | `false` | macOS 桌面版只在菜单栏显示图标、不显示 Dock 图标，关闭窗口后服务继续运行；在设置页修改立即生效 |
| `REQUEST_BODY_LOG` | `false` | 保存公开 API 请求与响应正文，各截断到 64 KiB，保留最近 1000 条 |
| `LOG_FILE` | `true` | 同时把服务日志写入 `runtime/logs/`，单文件 10 MB 轮转 |
| `LOG_MAX_TOTAL_MB` | `500` | 日志目录总量上限，超出时删除最旧的文件，不小于 20 |
| `ADMIN_AUTH_ENABLED` | `false` | 管理控制台账号密码登录开关 |
| `ADMIN_USERNAME` | `admin` | 管理员账号 |
| `ADMIN_PASSWORD` | 空 | 管理员密码，开启登录时必填 |
| `PROXY` | 空 | Chrome 导入、登录和账户默认使用的 HTTP、HTTPS 或 SOCKS5 代理，多个代理用逗号分隔组成代理池；留空时使用系统代理 |
| `INIT_TIMEOUT` | `2m` | 单账户 WAA 初始化超时 |
| `REQUEST_TIMEOUT` | `30m` | 单次请求最大执行时间，自动续写的每一轮开始时重新计时 |
| `WARM_WORKER_LIMIT` | `0` | 常驻预热账户数；0 为按需启动，空闲 30 秒后回收全部 Worker |
| `MAX_ACTIVE_WORKERS` | `1` | 高峰期活动 Worker 上限 |
| `WARM_STARTUP_CONCURRENCY` | `1` | 同时冷启动的 Worker 数，两种后端均适用 |
| `PER_ACCOUNT_CONCURRENCY` | `1` | 单账号同时执行的请求数 |
| `ROUTING_STRATEGY` | `round-robin` | `round-robin` 轮询；`fill-first` 账号粘性优先 |
| `ACCOUNT_ORDER` | 空 | `fill-first` 依次使用的账户，逗号分隔，未列出的账户排在后面并按 ID 排序；也可在设置页调整，保存后立即生效 |
| `MAX_ACCOUNT_SWITCHES` | `3` | 上游返回 403、5xx 等非额度错误时一个请求最多换几次账户重试，`0` 为不换号；额度用尽不计入，照常依次尝试其余账户 |
| `UPSTREAM_CHANNELS` | `playground,build` | 保留固定通道入口；`build` 为旧代理通道，上游已停用其生成，错误以上游为准 |
| `MODEL_MAPPING` | 空 | 客户端模型名到目录模型的映射，每项写成 `别名:模型 ID`，逗号分隔多项 |
| `MODEL_CHANNELS` | 空 | 限定模型只走一个通道，每项写成 `模型 ID:playground` 或 `模型 ID:build`；也可在“模型目录”的模型详情中选择，保存后立即生效 |
| `MODEL_ACCOUNTS` | 空 | 模型可用的账户，每项写成 `模型 ID:账户\|账户`，未列出的模型可用全部账户；也可在“模型目录”的模型详情中勾选，保存后立即生效 |
| `BUILD_NATIVE_NONSTREAM` | `true` | 自动路由的非流式请求优先使用 Build 原生调用；URL 固定通道优先 |
| `NONSTREAM_KEEPALIVE` | `true` | 非流式请求等待结果超过 10 秒时先返回 200，之后每 10 秒写出一个换行直到写出 JSON 正文，经 Cloudflare 等反向代理时连接不会因长时间无数据断开；此后才出现的错误以协议错误正文返回 |
| `FAKE_STREAM` | `false` | 流式请求先按非流式取得完整结果再按流式格式输出，中途失败时换号重发，等待期间照常发送心跳 |
| `YOUTUBE_LINKS` | `false` | 用户消息文本中的 YouTube 链接作为视频发送给模型，并从文本中移除 |
| `STRIP_THOUGHT_SIGNATURES` | `false` | 发往上游前去掉对话历史中的思考签名，函数调用带 `skip_thought_signature_validator`；之前各轮的思考内容不随签名发给模型，长对话的输入 token 较少 |
| `SEARCH_MODEL_ALIASES` | `true` | 提供支持搜索的模型的 `-search` 型号，使用该型号时启用 Google 搜索 |
| `DISCOVER_HIDDEN_MODELS` | `false` | 同步账户模型目录时（启动、每 24 小时与账户页“刷新全部”），找出 AI Studio 官网前端已配置、账户模型列表尚未列出的模型，经上游确认可用后加入目录，可直接用于各协议请求并列在 `/v1/models` 中，“模型目录”页标为“官网隐藏”；模型被正式列出后使用官方条目，关闭后下一次启动生成服务时从目录中移除 |
| `GOOGLE_SEARCH` | `false` | 为 OpenAI、Anthropic、Gemini 与 Interactions 的生成请求启用 Google 搜索，包含普通型号与携带函数工具的请求 |
| `ANTI_TRUNCATION` | `false` | 公开抗截断模型变体开关，保存立即影响后续目录与请求；专用接口仅解析别名，文本生成启用函数包装，独立于自动续写 |
| `CONTINUATION_REWRITE` | `true` | 以 assistant 消息结尾的请求在不支持原生续写的模型上改写为续写请求；关闭时所有模型原样发送，上游拒绝时返回其错误 |
| `AUTO_CONTINUATION` | `true` | Playground 分轮自动续写；关闭后不再发送下一轮，即使上游明确要求续写或达到输出上限。管理页保存立即生效，不中断已发出的轮次，与输入前缀改写无关 |
| `CONTINUATION_PROMPT` | 内置英文续写句 | 改写时与末尾 assistant 消息一起写入系统指令的提示词；设为空时末尾 assistant 消息改作 user 消息发送 |
| `FIRST_EVENT_HEDGE` | `off` | 原请求迟迟没有首字时用空闲账户（其他账户优先，没有时用原账户的空闲槽位）发起一个备用请求，先出首字的一方继续、另一方取消；`auto` 按各模型与通道近 24 小时首字等待的 p95，请求少于 20 次的模型与通道不发起；正整数为等待秒数，设置页选自定义时填入请求最多的模型的近期 p95 |
| `FIRST_EVENT_HEDGE_MODELS` | `gemini-4-argon` | 发起备用请求的模型 ID，逗号分隔；仅在 `FIRST_EVENT_HEDGE` 非 `off` 时使用，备用请求同样计入账户额度 |
| `WAA_BACKEND` | `spidermonkey` | 默认在进程内执行 WAA，不启动浏览器；可选择 `camoufox`，旧配置 `go` 自动迁移 |
| `MEDIA_OUTPUT` | `inline` | Chat Completions 与 Messages 正文中生成图片与音频的引用方式：`inline` 以 data URI 内联；`url` 按内容命名保存到 `runtime/media/`，以本服务的 `/media/<文件名>` URL 引用，该路径不校验 API key；目录总量超过 1 GiB 时从最久未使用的文件开始删除 |
| `MEDIA_BASE_URL` | 空 | 媒体 URL 使用的服务地址，例如 `https://ai.example.com`；留空时使用每个请求访问的地址，经反向代理时跟随 `X-Forwarded-Proto` |

服务启动时载入全部账户元数据，但默认不创建常驻 Worker。四项容量设置保存后立即作用于后续调度；启动并发限制对 SpiderMonkey 和 Camoufox 都生效，峰值上限调低时关闭多出的空闲 Worker，常驻数调高时补足预热。低内存默认值不强制覆盖用户已保存的配置。

两个后端的页面都固定使用 `temporary=true`，不再提供 `TEMPORARY_CHAT` 配置，也不发送额外的 Hello 预热生成。零热池不等于启动或刷新目录完全不创建页面；PageCatalog 的临时页面、重载与请求 Worker 共用活动及启动预算，满载时不会另开预算外页面。

首字备用请求仅作用于 `FIRST_EVENT_HEDGE_MODELS`，也受空闲 Worker 与单账户并发预算约束；默认 `0/1/1/1` 不会为备用请求额外启动浏览器或突破容量上限。已有文件显式设置的请求超时与双通道值保留，不按旧默认值删除。API key 列表与账户绑定保存后同时热更新；删除或轮换的旧 key 不再接受新请求，已准入请求不会因轮换或收紧绑定扩大原有账户范围。

用量页分别展示 `requested_model`（客户端请求模型）和 `response_model`（实际响应模型），保留模型映射可追踪性。未授权请求不展示模型、账户、正文或原始路径；日志大详情仅在展开时渲染，正文需要显式加载，折叠后释放格式化视图。`REQUEST_BODY_LOG=false` 仍是默认值。

### 端口配置

管理控制台默认通过本机回环地址免密访问。远程管理时：

- 设置 `ADMIN_AUTH_ENABLED=true`、管理员账号和密码，重启程序后通过独立 `/login` 页面登录
- 登录会话有效期为 12 小时；勾选“30 天内保持登录”时为 30 天，程序重启后仍有效
- 支持退出登录，管理员共享同一个账户池、配置与日志

公开 API 使用独立的 `PROXY_API_KEY`。

反向代理使用 HTTPS，并保留原始 `Host`、设置 `X-Forwarded-Proto: https`。管理页“服务配置”可调整登录开关及凭据，保存后重启程序生效。

API 试用页的 HTML 预览运行在 `PREVIEW_LISTEN_ADDR` 提供的独立来源，页面以当前访问的主机名加该端口打开预览。从其他电脑访问时，`PREVIEW_LISTEN_ADDR` 与 `LISTEN_ADDR` 一样监听可访问的地址；经反向代理访问时，在同一主机名的同一端口号上再代理到 `PREVIEW_LISTEN_ADDR`，例如管理页面为 `https://ai.example.com` 时，代理 `https://ai.example.com:2050` 到本机 `2050`。

- **管理页面与 API**: 默认端口 `2048`
- **HTML 预览**: 默认端口 `2050`
- **Camoufox**: 由程序动态分配本机端口

## 高级功能

### 代理配置

支持通过 HTTP、HTTPS 或 SOCKS5 代理访问 AI Studio，代理需要认证时在 URL 中填写账号密码，例如 `socks5://user:password@host:1080`：

1. 在“服务配置”中设置全局代理；留空时使用系统代理，先读 `HTTPS_PROXY` 环境变量，未设置时读 Windows 或 macOS 的系统代理设置，代理输入框下方显示当前生成服务实际使用的出口
2. 全局代理可以填写多个，用逗号分隔组成代理池：没有专用代理的账户固定绑定到绑定账户最少的可用代理，绑定写入账户状态，重启后不变；代理连续 3 次网络错误或定时检测失败时，其账户改绑到其他可用代理，检测恢复后重新参与分配；“服务配置”列出各代理的状态与绑定账户数，账户卡片显示每个账户的出口
3. 在“账户”页面编辑单个账户时可以设置账户专用代理
4. 账户代理同时用于登录、WAA 与业务请求
5. 带账号密码的代理由程序在本机回环地址提供无认证 SOCKS5 中转供 Camoufox 使用，账号密码不写入浏览器 profile；中转运行期间，本机其他进程也能经它使用该代理

### 认证文件管理

认证文件默认存储在 `auth/` 目录：

| 路径 | 内容 |
| --- | --- |
| `auth/<Google 邮箱>/account.json` | 账户邮箱、代理、语言、时区和启用状态 |
| `auth/<Google 邮箱>/storage-state.json` | Google Cookie 与认证续签材料 |
| `auth/<Google 邮箱>/runtime-state.json` | 权益等级、模型资格、冷却状态与资源所属账户 |
| `auth/<Google 邮箱>/pairing-renewal.json` | 服务端续签 token 的 SHA-256 摘要与最近续签时间 |
| `auth/<Google 邮箱>/camoufox-cache/` | 该账户浏览器的网页缓存与启动缓存，服务停止时可删除 |
| `auth/.leases/<Google 邮箱>.lock` | 同一账户目录的跨进程占用锁 |
| `[用户缓存]/AIStudio2API/runtime-leases/<Google 邮箱>.lock` | 当前电脑上该邮箱的 WAA Worker 占用锁 |

账户邮箱同时作为目录名、管理页面标识和日志来源，统一使用小写形式。`.leases` 协调账户目录读写，用户缓存中的 runtime lease 保证同一邮箱在当前电脑上只有一个 WAA Worker。

账户页的“添加账户”支持从 Chrome 导入、粘贴 Cookie、从指纹浏览器导入、隔离 Camoufox 登录和远程添加：

- 隔离 Camoufox 登录在本机打开可见窗口；服务器没有图形界面或从其他电脑访问时，登录画面显示在管理页面的登录窗口中，经反向代理访问时需要转发 `/api/login-view/` 的 WebSocket
- 添加与重新登录账户在服务端的任务中运行：提交后即可离开对话框，账户页上方逐个显示进度、结果与失败原因，关闭或刷新管理页面后任务继续；页内登录窗口可以隐藏，之后从任务行再次打开
- 粘贴 Cookie 或选择 Cookie 文件后自动识别其中登录的账户；Edge、Brave 等日常使用的浏览器用 Cookie 扩展导出后粘贴
- `ready` 账户可以编辑、停用、立即刷新和删除，`auth_required` 账户可以重新登录
- “刷新全部”立即读取全部启用账户的权益与模型目录，后台每 24 小时也会读取一次；请求的模型不在目录中且距上次读取超过 10 分钟时，服务先读取一次再处理该请求
- 从未读取成功的账户权益显示为未知，并显示最近一次失败原因
- 读取报告 AI Studio 不支持当前出口地区时，已有目录的账户同样显示该原因
- Google 限制账户使用 AI Studio、账户未满足 AI Studio 的年龄要求或所属 Workspace 没有开放 AI Studio 时，账户进入 `restricted`（服务受限）状态并显示原因与官网页面链接：启动、调度与后台读取都跳过该账户，不续签，验证时只载入一次页面，读取成功后恢复；从 Chrome、Cookie 与指纹浏览器导入时，服务受限的账户标出原因且不能导入

## 详细文档

- [开发与贡献](docs/development.md)
- [Google AI Studio 协议规范](docs/protocol.md)
- [Build 通道](docs/build.md)
- [运行日志说明](docs/logging.md)
- [可复用逆向开发指南](docs/reverse-engineering.md)

## 重要提示

### 关于 Camoufox

本项目使用 [Camoufox](https://camoufox.com/) 浏览器来降低被检测为自动化脚本的风险。Camoufox 基于 Firefox，通过修改底层实现来保持真实的设备指纹。

Go 负责编码、调度、流式解码与公开协议；选择 `WAA_BACKEND=camoufox` 时，受 WAA 保护的 `GenerateContent` 通过账户固定指纹 Camoufox 页面发送，保留原生 Firefox TLS/HTTP2、请求头、Cookie 与页面指纹。

`WAA_BACKEND=spidermonkey` 时，WAA 在服务进程内的 SpiderMonkey 中运行，按账户指纹模拟 Firefox 页面环境并以 Firefox 请求头直接发送，运行时不下载也不启动 Camoufox。账户页的浏览器登录仍使用 Camoufox，首次登录时按需准备。

### 使用限制

- **客户端管理历史**: Chat、Anthropic 和 Gemini 请求由客户端提交完整对话上下文
- **AI Studio 历史**: Playground 页面使用临时对话；已移除的 Build 写代码助手不再创建、续接或巡检上游应用，旧应用如需清理应在 AI Studio 官网管理
- **续接与恢复**: 已保存的终态 Responses / Interactions 节点写入 `runtime/states.db`，保留范围内的 `previous_response_id` / `previous_interaction_id` 重启后可查询和续接；未保存、已删除或淘汰的节点不可恢复。进行中的生成任务和账户任务不恢复，不能把终态持久化当作断点续跑
- **认证有效期**: 本机 Chrome 导入保留 OAuth 续签材料，需原 Windows 电脑执行续签。远程实例使用 `setup --remote` 的前台定时续签，重启原电脑后用 `--renew` 恢复；没有可用续签来源时重新导入或登录。普通 Cookie 导入不取得 Chrome OAuth 或设备密钥

## 贡献

欢迎提交 Issue 和 Pull Request！

## 📈 Star History

<a href="https://star-history.com/#Mag1cFall/AIStudio2API&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=Mag1cFall/AIStudio2API&type=Date&theme=dark" />
    <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=Mag1cFall/AIStudio2API&type=Date" />
    <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=Mag1cFall/AIStudio2API&type=Date" />
  </picture>
</a>
