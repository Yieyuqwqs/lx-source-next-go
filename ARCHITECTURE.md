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

## 9. 文件路由：用标准 HTTP 语义取代自签临时链

原版用 `Sign / Acip / Dyna / Expi` 自签临时链来保护缓存文件。现在改为：

| 原版自造 | 现在 |
|---|---|
| `Sign` 签名密钥 | 不需要——路径本身就是内容寻址 |
| `Expi` 临时链过期 | `Cache-Control: public, max-age=<Data.TTL>` |
| `Dyna` 校验链接 | `ETag` + `If-None-Match` → 304 |
| `Acip` 绑定 IP 入签名 | 需要时叠加 `auth.BanList` 中间件 |
| 无 | `Range` / `If-Range` → 206 断点续传 |

实现就是 `http.ServeContent`：它把 `Range` / `If-Range` / `If-None-Match` /
`If-Modified-Since` 全部处理妥当，我们只负责设置 `Content-Type`、`ETag`、
`Cache-Control` 并打开文件。

```
GET /api/v1/file/{source}/{musicid}/{name}
                                └── {quality}.{ext}
```

### 路径穿越的两道闸

1. **`buildFileKey`**：逐段白名单校验（`[A-Za-z0-9._-]`），拒绝空段、`.`、`..`、点开头、超长段。
   之所以必须自己做：`ServeMux` 只做路径规范化，编码过的 `%2e%2e`、`%00`、`%5c`
   解码后仍会原样落进 `PathValue`。
2. **`cache.File.rel`**：驱动内部再校验一次。
   两道闸刻意重复——安全边界不该只依赖上游一处判断。

`ETag` 由 `大小-mtime` 构造强校验器，内容一变必然变；
`If-Range` 不匹配时 `ServeContent` 会自动忽略 `Range` 返回 200 全文。

## 10. v0 契约层

```
GET /api/v0/{method}/{source}/{id}/{quality}
     │        │        │      │      └─ 可空；空时保留尾部斜杠
     │        │        │      └─ 音质
     │        │        └─ 资源 ID
     │        └─ 平台二字码 → source.Registry
     └─ url|link lrc|lyric mov|mv mid vid pic
```

### 方法别名表是唯一分发真源

`v0Aliases` 一张表决定全部方法路由。加别名只改表，handler 不动。

### `?jmp` 302

| 方法 | 带 jmp 的行为 |
|---|---|
| `url` / `link` | 302 → 播放直链 |
| `mov` / `mv` | 302 → 视频直链 |
| `pic` | 302 → 封面地址 |
| `lrc` / `lyric` | **直接吐 text/plain**（正文 + 翻译 + 罗马音） |
| `mid` / `vid` | 忽略 jmp，返回 JSON |

出错时**不跳转**，仍返回 JSON 错误——这是文档约定，播放器依赖它判断失败。

302 响应显式带 `Cache-Control: no-store`：直链有时效，不能被中间层缓存。

### 链路全貌

```
请求 → 客户端地址 → 访问日志 → panic 兜底 → 黑白名单 → 密钥 → 限流
     → handleV0 → 方法分发 → 来源查找 → 缓存读穿 → Source → 响应
                                      └─ 命中：不碰上游
```

### 内部缓存键隔离

v0 的 url/lyric 结果缓存在 `_meta/...` 命名空间下。
文件路由显式拒绝 `_` 开头的首段，避免内部元数据被当音频下载。

### mock 来源

`pkg/source/mock` 满足同一个 `source.Source` 接口，提供确定性数据，
并支持用特殊 ID `notfound` 触发 404，用于验证错误链路。
接真实平台时从注册表移除即可，handler 零改动。

## 11. 上游协议层：以 kw 为例

### 分层：纯函数与网络彻底分离

```
pkg/source/kw/
  cipher.go   纯函数  DES-ECB 加解密、q 参数构造        零 HTTP 引用
  quality.go  纯函数  音质 ↔ 酷我 br 参数                零 HTTP 引用
  parse.go    纯函数  响应报文 → 结构体（容错多形态）    零 HTTP 引用
  client.go   唯一做 IO 的地方，HTTP 执行器可注入        ← 只有这里碰网络
```

`Client` 通过 `Doer` 接口执行请求，因此测试可以注入
- `fakeDoer`：断言请求细节、返回固定报文；
- `httptest.Server`：走真实 `http.Client`，闭合整条链路。

### 已确认 / 待确认

| 项 | 状态 |
|---|---|
| DES-ECB + PKCS#5 + Base64 算法 | ✅ 已实现，往返测试通过 |
| 音质 ↔ `br` 参数映射（128k/192k/320k/flac/flac24bit） | ⚠️ 基于公开实现的常见取值，待抓包核对 |
| `mobi.s` 的 `f=kuwo&q=<密文>` 形态 | ⚠️ 同上 |
| 取链参数 `type/br/rid/format/sig/p2p/corp/user` | ⚠️ 同上 |
| 响应 JSON 形状 | ⚠️ 已做容错解析，覆盖 4 种已知形态 |
| 歌词接口路径与参数 | ❌ 未确认，路径可配置 |
| 视频 / 封面 / 音乐详情 | ❌ 未实现，返回明确错误而非瞎猜 |

**所有不确定项都不硬编码在代码里**：密钥、路径、有效期都是 `Config` 字段，
一旦拿到真实报文，改配置或收敛解析即可，不用重写逻辑。

### 测试闭环怎么做的

`TestWithHTTPTestServer` 里的假上游会**真的用 DES 解密我们发出的 `q` 参数**，
校验 `type/rid/br` 字段后再返回 JSON。因此这一个测试同时证明了：

1. 请求真的发了出去（走真实 `http.Client`）；
2. 我们生成的密文对端可解（算法与填充正确）；
3. 参数名与取值符合协议预期；
4. 响应解析与类型映射正确。

`TestKwFullChain` 在此基础上再往上一层：kw Source → 路由 → 缓存 → 响应，
并断言第二次请求命中缓存、上游调用次数不再增长。

### 未配置时的行为

`Config.BaseURL` 或 `Key` 缺失时，`Client` 直接返回 `503 + 明确说明`，
**不会发起注定失败的请求**，也不会伪造数据。

### 实测结论：antiserver 会忽略 `br`

在 GitHub runner 上对同一首歌分别请求 128k / 320k / flac / flac24bit，
四次返回的是**同一个资源文件**，只有 CDN 签名前缀不同：

```
128k      → .../nf/resource/n1/69/32/588957081.mp3
320k      → .../nf/resource/n1/69/32/588957081.mp3
flac      → .../nf/resource/n1/69/32/588957081.mp3
flac24bit → .../nf/resource/n1/69/32/588957081.mp3
```

由此做了两处修正：

1. **不再谎报音质**：`anti` 策略固定上报一个保守音质（默认 `128k`，可通过
   `Config.AntiQuality` 调整）。拿到的是 mp3 就绝不能对外声称 flac24bit。
2. **缓存键归一化**：新增可选接口 `source.QualityNormalizer`，
   上层用它把请求音质折成该来源真正支持的档位后再做缓存键。
   否则同一份文件会被存成四份、并产生四次上游调用。

取舍说明：宁可低报音质，也不高报。想要真实音质需要启用 `mobi` 策略 + 密钥。


## 12. 酷我匿名能力矩阵（实测）

用 `scripts/probe-kw.sh` 在 GitHub runner 上实测，共四轮：

| 能力 | 入口 | 结果 |
|---|---|---|
| 取播放直链 | `antiserver.kuwo.cn/anti.s` | ✅ 可用，**免密钥** |
| 搜索 | `search.kuwo.cn/r.s` | ✅ 可用，返回真实 ID/歌名/歌手/时长/格式 |
| 歌词 | `m.kuwo.cn/newh5/singles/songinfoandlrc` | ❌ `音乐查询失败`，加 UA/Referer 无效 |
| 歌词（老接口） | `newlyric.kuwo.cn/newlyric.lrc` | ❌ `TP=ERROR REQUEST`，接口已废 |
| 网页版 API | `www.kuwo.cn/api/www/...` | ❌ `The request is illegal!`（需 csrf/cookie） |
| mobi 免密钥伪装 | `mobi.kuwo.cn/mobi.s?f=web` | ❌ 403 |
| `format=flac` | anti.s | ❌ `refuse request!` |
| MV | anti.s `format=mp4` | ❌ `res not found` |
| 封面 | `artistpicserver` | ❌ 403 |

**结论：酷我的匿名天花板 = 搜索 + mp3 取链。**
歌词、封面、MV、音乐详情都需要凭据，启用 `mobi` 策略 + DES 密钥才可能突破。

### 两个只有真实数据才能发现的 bug

1. **`abslist` 定位错误**：真实响应是 `'abslist':[{`（键带引号），
   而我的实现按 `abslist:[` 匹配，导致 `total` 对、`items` 恒为 null。
   我自己的 fixture 也写成了错误形状，所以单元测试没抓到。
   → 修法：先定位 `abslist` 关键字，再取其后第一个 `[`。
2. **歌名带 HTML 实体**：返回的是 `花海&nbsp;(DJ&nbsp;阿若版)`，
   需要 `html.UnescapeString`。

这两个都是"Mock 测试全绿、真实数据立刻翻车"的典型，印证了探测优先于猜测。
