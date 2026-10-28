# 最低内存运行时

## 默认配置与旧部署迁移

新安装默认不常驻指纹浏览器，也不预热 WAA Worker：

```dotenv
WAA_BACKEND=go
WARM_WORKER_LIMIT=0
MAX_ACTIVE_WORKERS=1
WARM_STARTUP_CONCURRENCY=1
PER_ACCOUNT_CONCURRENCY=1
REQUEST_BODY_LOG=false
```

**已有 `.env` 和进程环境中的显式配置不会被静默覆盖。** 旧部署应在管理页面或 `.env` 中修改上述值，然后停止并重新启动生成服务；Docker 的进程环境变量优先于数据目录的 `.env`。不要把 `MAX_ACTIVE_WORKERS` 设为 0。

- 启动服务只准备账户与模型目录。首个需要 proof 的请求才创建纯 Go WAA VM，不下载、不启动 Camoufox；后续活跃请求复用 VM。
- 零热池每 3 秒检查一次，Worker 空闲 30 秒后可全部释放，包括最后一个。持有请求/独占租约的账户不会被空闲回收。
- 两种后端都遵守 `WARM_STARTUP_CONCURRENCY`，不再让纯 Go 并行冷启动绕过限制。
- 零热池跨账户替换时先关闭空闲旧 Worker，再启动新 Worker，避免同时保留两份完整 VM；启动失败时不保留旧热实例，下次请求可重新创建。
- 本地分词模型按需加载，空字符串计数不加载模型；零热池模式同时解除空闲模型的强引用，下次计数时重新加载，不改成粗略字符估算。
- 仅在回收器实际释放资源后调用 `debug.FreeOSMemory()`；不会逐请求强制 GC，也不会在空闲时无条件循环强制 GC。
- Go VM 关闭时中断残留脚本、等待事件循环退出并解除 VM/回调引用。初始化取消也会中断 VM；HTTP 客户端在失败或关闭时释放空闲连接。

**代价：** 休眠后首个请求需要重新请求 challenge、初始化 VM 并等待 WAA settle；分词模型也可能重载，首包延迟和 CPU 开销增加。默认请求并发为 1，高峰流量会排队。需要低延迟或吞吐量时可提高热池、活动上限与请求并发；热池大于 0 时仍保留原有的超额 Worker 空闲 5 分钟回收策略。

`MAX_ACTIVE_WORKERS` 是调度容量，不是整个进程的内存硬上限。同账户 VM 到期刷新、失败替换、请求正文/响应、大上下文和媒体处理仍可能产生瞬时峰值。

为支持中途断流后安全换号，非流式请求在提交结果前暂存上游事件，内存需求随输出大小增长；失败尝试的暂存数据会丢弃。流式请求仍增量转发并保留背压，不为重试缓存整段输出。

## 是否完全不需要浏览器

已有有效 `storage-state.json` 的账户可直接使用纯 Go 生成链路。保留 `WAA_BACKEND=camoufox` 作为兼容选项，但默认不用。

管理页面的**浏览器登录和 Cookie 导入验证**仍按需准备 Camoufox，结束后关闭；也可以从已有登录态文件导入，或在另一台机器完成登录后远程配对导入，避免服务器启动浏览器。账户文件中的指纹是少量静态数据，与常驻浏览器进程不同；它仍用于 WAA 宿主兼容，不删除。Docker 构建时打包匹配版本与字体/Xvfb 并不启动浏览器。更改默认后端并不等于取消 Google 的 WAA 验证，详见 [WAA 实现](waa.md)。

## 活跃运行时优化

### 纯 Go

- Firefox 形状表不再在每个账户中通过 `JSON.parse` 生成数万个完整 JS 对象。共享只读 Go 数据，通过各 VM 的轻量动态对象/数组读取；原始属性顺序保持不变。
- 形状数据使用弱缓存，没有活跃使用者时可被 GC 回收；DOM、JS 构造器、原型、Cookie 和账户状态不共享。
- DOM、Realm、Intl、提示词宿主字节码按需编译并共享；全局键顺序的只读数据共享，各 Realm 的解析状态独立。
- goja 采用上游独立模块与懒宿主模板；每个 runtime 的随机 Closure 名称通过私有视图覆盖，不改写共享全局键序表。
- 原生函数源码与 TrustedScript 标记使用弱键，避免注册表强引用历史对象。
- 短计时器注册与回调取锁串行，避免先触发后入表而滞留。
- 页面图片探测读入 `io.Discard`，不再为只需要判断非空的响应保留整张图片。

### 可选 Camoufox

保留第一轮优化：无头实例不预启动备用内容进程，HTTP 内存缓存限制为 16 MiB，图像 surface 缓存上限为 32 MiB，不缓存旧页面 viewer。账户 HTTP 磁盘缓存与有头登录默认行为保持不变；不关闭沙箱、站点隔离、WebGL、图片或字体。

受保护请求采用上游 `XMLHttpRequest`，保持官网网络行为。额外 UTF-8 编码/BiDi 队列每块最多 64 KiB、最多 8 块 credit，消费完一块后才归还 credit，满时暂停额外编码和推送。这一队列最多 **512 KiB 原始数据，约 683 KiB Base64**，不包含当前编码/序列化等开销。

**XHR 原生 `responseText` 仍随响应累积，不能用 credit 暂停浏览器接收或释放已收文本。512 KiB 不是 XHR 响应、完整浏览器或进程 RSS 上限。** 完成、网络失败、取消、关闭会解除 XHR/句柄/队列和等待项引用；消费者在 XHR 完成后取消也会主动唤醒背压等待者。默认 Go 后端不受这份浏览器原生副本影响。

管理端不预渲染折叠的请求/阶段详情或原始 JSON。用量正文需要显式加载，各正文展开后才格式化；切换记录、关闭组件时取消未完成的正文请求。折叠日志详情会释放正文和格式化视图。

## 离线内存对比

环境：Windows amd64、Go 1.27.0、Ryzen 7 H 260，2026-10-28 升级后复测。以下为无账户、无网络的宿主基准，**不是完整服务 RSS、旧 HEAD 与新服务的对比或真实生成请求峰值**。同一新版懒模板下比较 legacy JSON 与紧凑形状路径，累计分配取三次 `-benchtime=3x` 的中位数。

| 测量 | legacy JSON | 紧凑形状 | 变化 |
| --- | ---: | ---: | --- |
| 公共数据预热后，新增顶层页面＋iframe 的存活堆 | 33.65 MiB | 9.24 MiB | 减少约 73% |
| 同一组宿主创建的累计分配量 | 75,794,762 B/op | 45,682,453 B/op | 减少约 39.7% |
| 同一组宿主创建的分配次数 | 1,180,074 次 | 1,045,057 次 | 减少约 11.4% |

存活堆增量排除了已预热实例持有的共享数据，不能作为单账户总内存；B/op 是累计分配量，不是常驻内存。测试保留形状表对照路径，可同机比较：

```sh
go test ./internal/waa -run TestRealmHostLiveHeap -v
go test ./internal/waa -run '^$' -bench '^BenchmarkRealmHostShape$' -benchmem -benchtime=3x -count=3
go test ./internal/camoufoxnative -count=1
go test ./internal/camoufoxnative -run '^$' -bench '^BenchmarkDecodeProtectedData$' -benchmem -benchtime=10x
go test ./...
```

同机的 8 个 64 KiB 块解码基准：`append-growth` 累计分配 2,039,808 B/op，`streamed-blocks` 为 524,288 B/op，Go 侧只保留当前一个最多 64 KiB 的解码块。这是同进程算法分配对照，不是总 RSS，也不包含 XHR 内建 `responseText`。

测试覆盖零预热、并行冷启动上限、忙碌/最近使用实例不被回收、租约释放、分词重载结果一致、形状表内容与键序一致、只读数据和跨 VM 隔离、VM 关闭/取消、弱键回收及响应背压。全量 Go 测试前需按 README 构建前端。

未使用真实账户执行在线 WAA/生成验证，未测量完整进程 RSS；当前环境没有 C 编译器且 `CGO_ENABLED=0`，未运行 race detector。

## 进一步控制部署内存

- 无需请求正文时保持 `REQUEST_BODY_LOG=false`，不打开不需要的管理浏览器窗口。
- 可在**进程环境**中设置 Go 标准参数 `GOGC=50`，以更多 GC CPU 换较小堆余量；本项目不覆盖用户的 Go runtime 参数。
- `GOMEMLIMIT` 是 Go 软内存目标，不是 RSS 硬上限，也不包括 Camoufox 子进程；过小会导致 GC 频繁执行，不应以它承诺固定内存运行。
- 对比实机效果时应固定账户数、后端、Worker 配置、请求长度与并发，并记录主进程和全部子进程在启动后、首个请求、负载中、空闲回收后的内存。
