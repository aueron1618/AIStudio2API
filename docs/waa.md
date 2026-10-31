# WAA 实现

WAA 为 AI Studio 的受保护请求提供 BotGuard proof。两种后端均执行官网下发的解释器与挑战，而不是自行重写 proof 算法。绑定字段见 [协议规范](protocol.md) 与 [Build 通道](build.md)。

## 后端边界

| 后端 | JavaScript 与页面环境 | 受保护请求 | 浏览器进程 |
| --- | --- | --- | --- |
| `spidermonkey`（默认） | 进程内 Firefox JavaScript 引擎，加项目提供的 DOM、事件和指纹宿主 | 账户固定出口的 Go HTTP，匹配 Firefox 网络形状 | WAA 阶段不需要 |
| `camoufox` | 真实 Camoufox 页面 | 页面 `XMLHttpRequest` | 需要 |

旧配置 `WAA_BACKEND=go` 在载入时规范化为 `spidermonkey`，不再保留 Goja 执行路径。浏览器登录和浏览器 Cookie 验证仍按需准备 Camoufox；`method=http` 的 Cookie 验证不执行 WAA，也不启动浏览器。

两种后端读取同一账户的 Cookie、`auth_user`、指纹与代理设置。多 Google 会话使用官网 `/u/N/` 页面和 `X-Goog-AuthUser: N`；重新浏览器登录后重置为会话 0。

## SpiderMonkey 链路

1. `internal/aistudio/runtime_go.go` 打开账户官网页面，读取公共协议参数，并调用 `Waa/Create` 获取挑战。
2. `internal/waa/runtime.go` 在线程固定的事件循环中创建 SpiderMonkey 上下文，加载官网解释器。
3. `internal/waa/host.go`、`dom.go`、`smhost.js` 与快照数据提供浏览器宿主；JavaScript、Intl 等语言行为使用原生引擎。
4. 每次请求同步提示词与页面输入事件，使用请求 binding 的摘要调用 snapshot，将 proof 写入协议规定的位置。
5. Go 网络层使用该账户固定出口发送请求，并保存轮换的 Cookie。

`internal/waa/mozjs` 是 CGO/C++ 绑定。引擎与完整浏览器不是一回事：DOM、页面状态与部分浏览器 API 仍由项目模拟，不能仅根据生成了 `!` 开头的字符串判断服务端一定接受 proof。

## 生命周期与资源

- 每个活动 Worker 独立持有 JS 上下文、DOM、构造器和原型，不在账户间共享可变页面状态。
- JS 操作在持有固定 OS 线程的事件循环上运行；微任务在任务后处理，计时器和任务队列有独立的关闭状态。
- 初始化取消和关闭会中断正在执行的原生脚本，等待事件循环退出后才返回，清除回调、句柄与原生上下文。
- 请求、槽位与 Worker 的取消仍由现有 Go 生命周期控制；Camoufox 流式传输保留有界分块和背压。
- 默认零热池、活动上限 1、冷启动并发 1；两种后端均遵守同一启动预算。空闲回收与分词器释放见 [运行时内存](memory.md)。

SpiderMonkey 使用原生内存，不能用 Go heap 或 `GOMEMLIMIT` 代替整个进程的内存指标。源码中的 JS 堆上限不是预分配量，也不是整个进程的内存上限。

## 构建与验证

需要 Go 1.27、CGO、目标平台 C/C++ 工具链及匹配的 SpiderMonkey 静态库。当前库版本与下载流程由 `.github/actions/spidermonkey/action.yml` 和发行工作流维护；本地步骤见 [开发文档](development.md) 与 README。发行程序静态链接引擎，运行者不需要安装编译器。

配置原生编译环境后执行：

```bash
go test -mod=readonly ./internal/waa ./internal/camoufoxnative ./internal/aistudio
go test -mod=readonly ./cmd/... ./internal/...
go vet ./...
```

离线测试覆盖宿主隔离、主要浏览器 API、Promise 顺序、取消中断、关闭释放和有界流控。它们不访问真实账户，不能替代真实官网挑战的通过率、延迟和进程 RSS 验证。
