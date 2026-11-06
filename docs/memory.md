# 运行时内存

本项目沿用上游 SpiderMonkey、原生 SQLite 与调度器结构，并保留按需启动和资源回收策略。旧 Goja 基准不代表当前原生引擎的内存或性能。

## 默认策略

```dotenv
WAA_BACKEND=spidermonkey
WARM_WORKER_LIMIT=0
MAX_ACTIVE_WORKERS=1
WARM_STARTUP_CONCURRENCY=1
PER_ACCOUNT_CONCURRENCY=1
REQUEST_BODY_LOG=false
```

- 零热池不创建常驻预热 Worker；显式启动或刷新需要的 PageCatalog 页面仍按需创建，取得目录后回收，生成请求再按需启动 Worker。
- 两种后端共用活动与冷启动并发限制，临时目录页、重载和请求 Worker 都计入预算，不因使用原生 JS 引擎而放开数量。
- 满载时先关闭可回收的空闲旧 Worker，再启动替代者，正数热池同样适用；同账户重载只有在独占维护窗口且预留额外容量时才允许新旧页共存。
- 约 30 秒空闲后回收零热池 Worker，正数热池仍采用 5 分钟空闲策略并维持预热目标。
- 分词模型按需解压载入，没有使用者且空闲后释放；长文本计数保留有界哈希缓存，不保存原始提示词。
- 发生空闲回收时归还空闲 Go 内存；原生 JS 上下文在 Worker 关闭时销毁。

已保存的正数热池、并发或正文日志设置继续生效，不被新默认值覆盖。旧 `.env` 中的 `WAA_BACKEND=go` 自动迁移为 `spidermonkey`，但构建已需要 CGO。

## 其他保留项

- Camoufox 无头模式限制缓存与资源使用，不通过关闭安全沙箱或禁用 WebGL 来省内存。
- 浏览器受保护响应按 64 KiB 编码并使用背压；消费后释放缓冲，失败、取消和关闭会清理 XHR 与回调。
- 账户 DOM、JS 原型和 Cookie 相互隔离，不共享可变页面状态。
- 大日志折叠时卸载详情；请求正文显式加载，关闭或切换记录时取消未完成的请求并释放原文与格式化副本。
- 默认不记录请求正文；启用后使用 gzip 压缩存储，但不加密。数据库按上游 schema 自动补列并清理未声明对象，旧 `bodies` 表不再额外迁移；当前 `request_bodies` 正文继续保留。
- `no-browser` Docker 目标保留 HTTP Cookie 导入与 SpiderMonkey，不包含 Camoufox 的系统依赖；默认 `browser` 目标支持浏览器功能。CI 的 `release-no-browser` / `release-browser` 复用同次构建的静态发行程序，运行时边界相同。
- HTTP Cookie 导入只验证并提交认证状态，不为同步新 PageCatalog 启动 WAA 或浏览器；目录由后续显式启动/刷新取得。两个后端页面固定 `temporary=true`，不发送额外 Hello 预热生成。

## 取舍与测量

零热池减少闲置占用，但首个请求需要冷启动。多账户或高并发负载可以提高热池和活动上限；提高之前应测量同时启动时的内存峰值。

SpiderMonkey 的原生堆、引擎映射和 SQLite 内存不完全体现在 Go heap 中。`GOMEMLIMIT` 不是整个进程限额；JS 堆上限也不是预分配量。至少观察进程 RSS、冷启动峰值、proof 延迟、并发峰值和空闲回收后的残留，不应直接与旧 Goja 的 `B/op` 数值比较。

准备 README 所述原生工具链后，可运行不使用真实账户的验证：

```bash
go test -mod=readonly ./cmd/... ./internal/...
go test -run '^$' -bench BenchmarkRealmHost -benchmem ./internal/waa
go test -run '^$' -bench BenchmarkDecodeProtectedData -benchmem ./internal/camoufoxnative
```

这些基准只描述对应的离线工作负载，不证明真实 BotGuard 挑战的通过率，也不代表峰值或整机 RSS。
