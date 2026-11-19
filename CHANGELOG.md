# 更新日志

## v0.1.6 — 收口：把「说了没做」的配置与缓存语义全部落地

本轮把全仓扫了一遍（死代码/未用导入/跨包引用/未定义方法/重复定义五项检查），并修掉语义类问题：

**缓存与协议语义**

- 歌词缓存**从不命中**：`Meta.Quality="lrc"` 被策略判成「未知音质」。改为「空音质 = 没有音质概念 = 可缓存」
- 缓存里可能是**已过期的直链**（TTL 1 小时、直链 30 分钟）：命中后先校验 `expi`，过期就作废并回源一次
- `/api/v1/file` 的 `Cache-Control` 与策略层不一致（0 时输出 no-cache）：统一到 `policy.DefaultTTL`
- `/api/v1/status` 在配置快照为空时会崩：加 nil 守卫

**配置项全部接线（此前解析了却不生效）**

| 配置 | 现在的行为 |
|---|---|
| `Main.Cors` | 装 CORS 中间件（含 OPTIONS 预检 204） |
| `Main.LogDir` | 日志同时落盘 `lxsrcs.log`（MultiWriter），控制台开关仍生效 |
| `Srcs.*.Enable` | 真实生效且**热更即改**：停用的源在 v0/搜索里回 503 |
| `Apis.ApiOnly` | 根路径自检页在 ApiOnly 时回 404 |
| `Data.Type` | 校验只允许 0（本地文件），不再静默忽略 |
| `Errs.ErrMp3 / Expi` | 取链失败按 v0 约定 302 到错误音频，并按 Expi 设缓存头 |

**其它**

- 新增根路径自检页（纯静态）、`pkg/middleware/cors`、`cache.Reader.Invalidate`
- 删掉测试专用的 `apierr.HTTPStatusOf`、`ipguard.CheckString`
- 新增 6 个测试：自检页 ×2、CORS 预检、来源停用、错误音频 302、空音质可缓存、Invalidate 回源

## v0.1.5 — 取链通道实测重排：网易云改用 weapi（匿名可用）

本轮把「哪个接口真能取到直链」重新实测了一遍（全部真实网络请求，不是推测）：

| 平台 / 方式 | 结果 |
|---|---|
| 酷我 `antiserver.kuwo.cn/anti.s` 直连 | ✅ 97B 纯文本直链，291ms |
| 网易云 官方 `api/song/enhance/player/url` | ❌ `code:-110, url:null` |
| 网易云 **weapi**（`weapi/song/enhance/player/url/v1`，匿名） | ✅ 免费曲目真链：128k、721,023 B、`ID3`，下载校验通过 |
| 网易云 付费曲目（186016 等） | ❌ `url:null`，如实报「会员曲目或无版权」 |
| 网易云 `song/media/outer/url` | ❌ 302 落到 `/404` |
| 网易云 借 GD 音乐台签名 | ✅ 320k、13,042,460 B、`audio/mpeg`、首字节 `ID3` |
| 酷狗 v2/v1 旧接口 | ❌ `status:2` / "The Resource Needs to be Paid" |
| 酷狗 **v5**（`trackercdn.kugou.com/v5/url`，客户端签名） | ✅ 免费曲目 `status:1`，3,196,769 B 下载成功；高音质/24bit 取决于曲目权限，按档位回退 |
| 腾讯 `musicu.fcg` vkey（旧） | ❌ 匿名 `purl` 为空（104003） |
| 腾讯 **`musics.fcg` + zzb 签名**（新） | ✅ 免费曲目 128k 真链：2,115,081 B、`ID3`，下载校验通过 |
| 腾讯 付费曲目 | ✅ 试听片段（`RS02` 前缀）可匿名取到；完整 320k/无损/24bit 需 `[Tx] Uin/Ukey` |
| 腾讯 第三方站（xwzkj / toubiec / vkeys-QQ） | ❌ 域名失效 / 反爬 JS 挑战 / 503 需登录池 |
| 咪咕 web `getPlayInfo` | ❌ 301 到 `/v5/` 前端页 |
| 咪咕 **App listenSong**（`MIGUM2.0/v1.0/content/sub/listenSong.do`） | ✅ 免费曲目 302 → 真链，4,447,696 B、`audio/mpeg`、`ID3` 下载校验通过 |
| 咪咕 vip 档（ZQ 24bit） | ⚠️ 匿名回「暂不提供试听地址」，需登录态 Cookie |
| 公共 Meting 实例（4 个） | ⚠️ 仅 1 个偶发可用，其余 521/超时 |
| 落月API `api.vkeys.cn`（无 token） | ❌ 元信息全给，但 `url` 一律 `null`（8 首歌 0 条给地址） |


改动：

- 新增 `pkg/source/wy/aggregator.go`：取链**先聚合、失败再回官方 eapi**；
  两条路都失败时把两边原因一起带回，不做静默换源
- 新增启动配置 `[Wy] Agg / AggURL`：**默认关闭**。
  公共实例从本机网络访问时通时不通（Cloudflare 握手失败/超时），
  默认打开等于给每次取链加几秒不确定延迟；自建 Meting 之类的签名服务后再打开才稳
  （`AggURL` 指过去 + `Agg = true`），关闭时行为与 v0.1.4 完全一致
- 新增 `[Tx] Cookie`：QQ 的 vkey 必须带登录态才给 `purl`（`uin=o...; qqmusic_key=...`），
  未填时保持匿名请求形状并如实报错；填好后可再把 QQ 加回自定义源脚本
- 按「不谎报」原则，自定义源脚本暂时移除 QQ（匿名必然失败），只留酷我 + 网易云
- 新增 5 个聚合单测（请求形状、音质以对端回显为准、回落官方、双失败文案、br 双向映射）
- 新增 6 个 weapi 单测、3 个 Cookie 单测（网易云 3 个 + QQ 3 个）、2 个 QQ 取链单测
- 新增 `pkg/source/qq/sign.go` + `vkey.go`：QQ 的 zzb 签名（MD5 → 固定下标抽取 → 掩码异或 → 自定义 base64），
  请求 `musics.fcg?format=json&sign=` 先取曲目信息再取 vkey；付费曲目自动退到试听片段（`RS02<vs>.mp3`）
- 新增 `pkg/source/mg/`：咪咕 App 接口（搜索 `search_all.do` + 取链 `listenSong.do`），
  按 ZQ(24bit) → SQ(无损) → HQ(320k) → PQ(128k) 逐档回退；搜索到的音质规格（含 24bit 45MB）直接透出
- 新增 `pkg/source/kg/v5.go`：酷狗 v5 取链（MD5 双重签名 + `Kg-Thash` 客户端头），
  搜索改用 `song_search_v2` 并记录每档音质的 hash（含 24bit 的 `ResFileHash`），
  取链按 flac24bit → flac → 320k → 128k 逐档回退，实报最终档位；脚本按音质挑 hash
- 新增 `pkg/source/wy/weapi.go`：**weapi 协议**（AES-128-CBC 两层 + RSA `encSecKey`），
  匿名即可取免费曲目的 128k 直链、零第三方依赖 —— 取链顺序变成 **weapi → 聚合 → eapi**
- 新增 `[Wy] Cookie`：填自己的登录态后官方接口能给出更高音质（匿名时以响应 `br` 为准如实上报），
  这是目前唯一稳的网易云方案；匿名时第三方只给元信息不给地址（见上表）
- 新增 `cookie_test.go`：匿名只带 `os=pc`、配了登录态要拼上且不重复

## v0.1.4 — 匿名取链能力普查（定论）

对 5 个平台的约 20 个公开接口做了全量实测，**结论：只有酷我能在匿名环境下取到播放直链**。

| 平台 | 搜索 | 歌词 | 播放直链 | 失败证据 |
|---|---|---|---|---|
| 酷我 kw | ✅ | ❌ | **✅** | — |
| 酷狗 kg | ✅ | ❌ | ❌ | trackercdn 全部 `errcode:20028`（需签名/设备 mid） |
| 网易云 wy | ✅ | ✅ | ❌ | `enhance/player/url` 返回 `url:null, code:404`（需 eapi） |
| QQ tx | ✅ | — | ❌ | `musicu.fcg` 返回 `result:104003, purl:\"\"`（需登录） |
| 咪咕 mg | ❌ | — | ❌ | 接口 301 到 `/v5/` 前端页 |

附带修复：

- `kg` 取链失败时错误信息明确说明原因，不再是笼统的「解析失败」
- 探测脚本（`scripts/probe-*.sh`）保留为手动工具，不再在每次 CI 中运行

## v0.1.3 — 接入第三方公开 API + Android DNS 修复

新增两个平台来源，全部走公开匿名接口，无需任何密钥：

| 平台 | 搜索 | 歌词 | 播放直链 |
|---|---|---|---|
| 酷我 kw | ✅ | ❌ | ✅ |
| 酷狗 kg | ✅ | ❌ | ❌ 该接口不含直链 |
| 网易云 wy | ✅ | ✅（含翻译与逐字） | ❌ 需加密 |

其它改动：

- **Android/Termux 的 DNS 修复**：静态二进制在 Android 上走纯解析器，
  读不到 `/etc/resolv.conf` 会导致所有域名解析瞬间失败
  （表现为「请求上游失败」而耗时只有几毫秒）。
  现在会自动回落到 `223.5.5.5:53`，也可用 `DNSServer` 显式指定。
- **错误根因透出**：`httpx.Fail` 现在会把底层错误拼进 msg，
  能看到 `dial tcp: lookup ... no such host` 这类信息。
- 酷狗搜索必须走 http（该域名在腾讯云 CDN 后面，证书不覆盖）；
  直链解析改为扫描所有字符串字段，不依赖具体字段名。

## v0.1.2 — 可导入播放器的自定义源脚本

- **新增 `GET /api/v0/lx-script.js`（别名 `/api/v0/script`）**：
  直接输出可导入 LX Music 的自定义源脚本。
  - 脚本内置在二进制里（`go:embed`），地址按请求推导，支持反向代理头
  - `?key=` 注入接口密钥；`?raw` 触发浏览器下载
  - **密钥与地址在嵌入 JS 前做转义**，防止带单引号的 key 闭合字符串造成注入
- 脚本只声明 `musicUrl` 动作——LX 的规范里非 local 源仅支持该动作，
  歌词由播放器自己的内置源负责，与我们的匿名能力正好对齐
- 冒烟脚本新增脚本下载、地址注入、密钥注入三个用例

## v0.1.1 — 修复首次运行体验

在 Termux（Android）上实测发现的问题：

- **首次运行自动生成配置**：`data/bootstrap.toml` 与 `data/dynamic.toml` 缺失时
  用内置模板生成（`//go:embed`），**绝不覆盖已有文件**。
  生成的模板会被真实解析校验，保证不是坏配置。
- **热重载错误去重**：配置长期不可读时，之前每 2 秒打一条 ERROR 刷满终端；
  现在同一错误文案只记一次，恢复后重置。
- 配置模板改为二进制内置的单一真源，仓库不再放示例文件副本；
  发布包也不再附带 toml（首次运行自动生成）。

## v0.1.0 — 首个可用版本

从零重建的 LX Music 自定义源后端骨架，**只依赖一个第三方库**（`go-toml/v2`）。

### 架构

- **分层单向依赖**：`cmd` 组合根 → `pkg/server` 装配 → `pkg/middleware/*` 策略 →
  `pkg/httpx` 传输 → 纯领域包。各层之间没有反向依赖。
- **无全局可变状态**：配置、限流计数、熔断状态、日志环全部由组合根创建并显式注入。
- **配置原子热重载**：`atomic.Pointer` 快照 + 内容哈希比对；
  解析或校验失败保留旧配置；重载器只认识 `Holder` 与 `Loader`，
  **类型层面不存在清缓存的路径**。
- **中间件可插拔**：`access` / `apikey` / `ratelimit` / `recoverer` / `reqlog`
  互相不 import，由 `server.Options` 四个开关控制装配。

### 能力

| 能力 | 状态 |
|---|---|
| 标准 HTTP 文件服务（`ETag` / `Range` / `Cache-Control` / 304 / 206） | ✅ |
| 缓存抽象 + 读穿 + 同键并发合并（防击穿） | ✅ |
| v0 契约（`url`/`lrc`/`mv`/`mid`/`vid`/`pic` + 别名 + `?jmp` 302） | ✅ |
| v1 搜索接口 | ✅ |
| 歌词五格式 LRC / LRCX / KRC / QRC / NRC | ✅ |
| 错误码统一映射 / 音质别名归一与回落 / 账号池熔断 / netip 黑白名单 / slog→SSE | ✅ |
| **酷我（kw）取播放直链** | ✅ 匿名可用，mp3 |
| **酷我（kw）搜索** | ✅ 匿名可用 |
| 酷我歌词 / 封面 / MV | ❌ 需凭据，如实返回 503 |
| 其他平台 | ❌ 未开始 |

### 关于酷我的实测结论

在 GitHub runner 上实测四轮：`antiserver.kuwo.cn/anti.s` 与 `search.kuwo.cn/r.s`
可匿名使用；歌词、封面、MV 的所有公开入口均已失效或需要 csrf/cookie。
且 `anti.s` **会忽略 `br` 参数**，固定返回同一个 mp3，
因此本实现对 anti 策略一律上报保守音质（默认 `128k`），绝不谎报无损。

### 验证

- 188 个测试函数；`go test -race` 全绿
- 端到端冒烟：真起服务 + 真实 curl，覆盖 200/206/302/304/400/401/404/429/503
- 歌词 KRC/QRC 用参考实现常量生成真实二进制做跨实现校验
