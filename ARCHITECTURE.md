# 架构说明

一句话：**依赖单向向下，状态集中在一处，策略在运行时注入。**

## 1. 分层

```
┌──────────────────────────────────────────────────────────────┐
│  cmd/lxsrcs              组合根：唯一 new 具体实现的地方       │
└───────────────────────────┬──────────────────────────────────┘
                            │ 显式注入
┌───────────────────────────▼──────────────────────────────────┐
│  pkg/server              装配：中间件顺序 + 路由             │
│                          （唯一知道"有哪些中间件"的地方）      │
└───────────────────────────┬──────────────────────────────────┘
                            │
┌───────────────────────────▼──────────────────────────────────┐
│  pkg/middleware/*        策略：access / apikey / ratelimit    │
│                          recoverer / reqlog                  │
│  pkg/middleware           Chain（组合原语，无业务）           │
└───────────────────────────┬──────────────────────────────────┘
                            │
┌───────────────────────────▼──────────────────────────────────┐
│  pkg/httpx               传输：响应信封 / 错误落盘 / 客户端 IP │
└───────────────────────────┬──────────────────────────────────┘
                            │
┌───────────────────────────▼──────────────────────────────────┐
│  pkg/apierr  pkg/ipguard  pkg/quality  pkg/balancer          │
│  pkg/logx    pkg/lrcutil                                     │
│  纯领域：无 HTTP、无 IO 副作用、可单独测试                    │
└──────────────────────────────────────────────────────────────┘

        pkg/config  ──── 横切：只被组合根与 server 依赖
        （原子快照；不认识 HTTP，也不认识缓存）
```

**依赖规则**：箭头只能向下。`pkg/quality` 不知道 `httpx` 存在，`httpx` 不知道 `config` 存在，
`middleware/*` 不知道 `server` 存在。没有任何包 import `cmd/`。

## 2. 中间件：装配表与插拔

`server.chain()` 里就是一张显式的表：

```
客户端地址解析 → 访问日志 → panic 兜底 → 黑白名单 → 密钥 → 限流 → 路由
```

装配与否由 `server.Options` 的四个开关决定：

```go
app := server.New(server.Deps{...})

// 生产：全开
Options{EnableRequestLog: true, EnableAccess: true, EnableApiKey: true, EnableRateLimit: true}

// 内网调试：只留日志和兜底
Options{EnableRequestLog: true}

// 换顺序：把限流提到密钥之前，只改 chain() 里两行的位置
```

**插拔为什么是真的**：每个中间件都是标准的 `func(http.Handler) http.Handler`，
彼此不 import，只认识自己需要的窄接口/函数。

| 中间件 | 依赖什么 | 不认识什么 |
|---|---|---|
| `access` | `ipguard` + `func() Config` | 密钥、限流、路由 |
| `apikey` | `func() Rules` | IP、限流、路由 |
| `ratelimit` | `Limiter` 接口 + `func() Rules` | IP 判定细节（只读 `httpx.Trusted`） |
| `recoverer` | `*slog.Logger` | 其它一切 |
| `reqlog` | `*slog.Logger` | 其它一切 |

**限流换实现**：`Limiter` 是接口，中间件只调 `Allow(key, limit, window)`。
把内存实现换成 Redis，`server.go` 改一行构造参数即可，中间件零改动：

```go
// 现在
Limiter: nil,
// 以后
Limiter: redis.New(redis.Config{Addr: "..."}),
```

## 3. 配置热重载：原子、幂等、不碰缓存

```
                ┌─────────────┐
  读字节  ────► │  Fingerprint│ ──同──► 跳过（连解析都省了）
                └──────┬──────┘
                       │ 异
                ┌──────▼──────┐
                │ Parse+Validate │ ──失败──► 返回错误，旧快照继续服务
                └──────┬──────┘
                       │ 成功，得到完整新对象
                ┌──────▼──────────────────┐
                │ atomic.Pointer.Store    │  ← 唯一写点
                └──────┬──────────────────┘
                       │
        读者（中间件/handler）读到的一定是完整快照
```

**四条保证**：

1. **原子**：写方只做一次 `atomic.Pointer.Store`。读者要么看到旧快照，要么看到新快照，
   不存在"一半新一半旧"。
2. **不可变**：`Snapshot.Dyn` 发布后不再被修改，热更是换新对象而不是原地改。
   因此读者不需要加锁，也不需要拷贝。
3. **失败即回退**：解析或校验任一步失败，直接返回错误，旧快照原样服务，
   日志记一条 `配置热更新失败，保留旧配置`。
4. **不清缓存**：`Reloader` 只认识 `Holder` 与 `Loader`，
   **它没有缓存字段可用**——"热重载会不会清缓存"这个问题在类型层面就不成立。

**为什么用轮询而不用 inotify**：按内容哈希比对天然幂等，
编辑器的"改名保存"、`sed -i` 的原子替换都不会漏事件；也不需要引入 fsnotify 依赖。
若要更低延迟，把 `Reloader.Once()` 接到任意事件源即可，`Reloader` 本身不用改。

## 4. 没有全局变量，状态在哪

| 状态 | 持有者 | 生命周期 |
|---|---|---|
| 启动配置 | `config.Bootstrap` 值拷贝 | 进程 |
| 动态配置 | `config.Holder` 内的 `atomic.Pointer` | 进程，可换 |
| 限流计数 | `ratelimit.Memory` 实例 | 进程 |
| 熔断状态 | `balancer.Breaker` 实例 | 进程 |
| 日志环 | `logx.Hub` 实例 | 进程 |
| 白名单编译缓存 | `access.compiler` 实例 | 进程，随配置变 |

全部由 `cmd/lxsrcs/main.go` 创建、通过 `server.Deps` 注入。
没有 `package-level var` 承载可变状态，因此**同一个进程里可以起两套互不干扰的实例**（测试就是这么做的）。

## 5. 扩展点

| 想做的事 | 改哪里 | 改动量 |
|---|---|---|
| 加一个中间件 | 新包 + `chain()` 一行 | 1 处 |
| 去掉/重排中间件 | `server.Options` 或 `chain()` | 1 处 |
| 限流换 Redis | `Deps.Limiter` | 1 行 |
| 配置来源换成 etcd | 实现 `config.Loader` | 1 个类型 |
| 配置改 JSON/YAML | 替换 `config.Parse` 内部实现 | 1 个函数 |
| 换日志后端 | `hub.Handler(next)` 的 `next` | 1 行 |
| 加一条路由 | `routes()` | 1 行 |

## 6. 目录

```
cmd/lxsrcs/            组合根
data/                  bootstrap.toml（启动）/ dynamic.toml（热更）
pkg/config/            分层配置、原子快照、重载器
pkg/server/            装配与路由
pkg/middleware/        Chain 原语
pkg/middleware/access/     IP 黑白名单
pkg/middleware/apikey/     接口密钥
pkg/middleware/ratelimit/  限流（Limiter 接口 + 内存实现）
pkg/middleware/recoverer/  panic 兜底
pkg/middleware/reqlog/     访问日志
pkg/httpx/             响应信封、错误落盘、客户端 IP
pkg/apierr/            错误码唯一真源
pkg/ipguard/           netip 黑白名单
pkg/quality/           音质别名与回落
pkg/balancer/          账号池熔断
pkg/logx/              slog → SSE 扇出 + 脱敏
pkg/lrcutil/           歌词五格式
```

## 7. 还没做

- 各平台上游协议层（唯一无法靠"研究+实现+自测"闭环的部分，必须真账号联调）
- 缓存驱动（`Data.Mode` 已进配置，驱动层未实现）
- 前端资源挂载（`Apis.ApiOnly` 已进配置，静态文件服务未接）

## 8. 缓存层（Data.Mode）

```
调用方 ──► cache.Reader ──► Cache 接口 ──► 驱动（File / Memory / Redis）
                │
                ├─ Policy：能不能缓存 + TTL（来自热更配置）
                └─ Group：同键并发合并，只打一次上游
```

三个关注点互相独立：

| 角色 | 只负责 | 不知道 |
|---|---|---|
| `Cache` | Get/Set/Del 与 TTL 落存 | 缓存什么、多久 |
| `policy.Dynamic` | 判定是否可缓存、给 TTL | 存在哪 |
| `Reader` | 读穿 + 合并 + 统计 | 存储细节、规则来源 |

**为什么换存储只改一行**：`Reader` 只依赖 `Cache` 接口，
`policy` 只依赖 `cache.Meta`，两者都不认识具体驱动。

### 热重载边界（本层最重要的一条）

```
配置变化 ──► policy 重新编译 ──► 之后写入用新 TTL / 新规则
                              ✗ 不清空缓存
                              ✗ 不改动已有条目
                              ✗ 不重新加载已有数据
```

这不是靠"记得别清理"来保证的：`cache.Reader` 与 `policy.Dynamic` **都没有删除或重建缓存的代码路径**。
`pkg/cache/reload_test.go` 里的 `TestConfigReloadKeepsCachedData` 把这条边界钉住了：
热更后同一个键仍然命中、上游调用次数不增加、旧条目按旧 TTL 继续存活，而新写入立刻用新 TTL。

### 防击穿

`cache.Group` 合并同键的并发未命中。与 `x/sync/singleflight` 的差别是：
加载上下文与首个调用方的取消脱钩（`context.WithoutCancel` + 兜底超时），
因此某个客户端断开既不会连累其它等待者，也不影响结果写回缓存。

### 目录归属

| 配置 | 归属 | 理由 |
|---|---|---|
| `Main.CacheDir`（`Bootstrap.CacheDir`） | 启动期 | 数据落在哪属部署形态 |
| `Data.Mode` / `Data.TTL` / `Data.Rule` | 热更期 | 缓存什么、多久属运营策略 |
