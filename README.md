# lx-source-next

LX Music 自定义源后端，从零重建：**五个平台的搜索与取链都已打通**，匿名可听免费曲目，
带凭据可上 VIP 音质（最高 24bit）。

**只依赖一个第三方库**（`go-toml/v2`）：AES/HMAC/RSA、DES、zlib、XML、netip、slog、HTTP 服务
全部走标准库手写。

架构说明见 [ARCHITECTURE.md](ARCHITECTURE.md) · 更新日志见 [CHANGELOG.md](CHANGELOG.md)

---

## 快速开始

```sh
make build            # 或 go build ./cmd/lxsrcs
./lxsrcs -conf data   # 默认监听 :1101
curl 'http://127.0.0.1:1101/api/v0/ping'
```

首次运行会在 `data/` 里**自动生成** `bootstrap.toml` 与 `dynamic.toml`，无需手动准备。
默认配置开箱可搜可播（五个源全部匿名可用，免费曲目）。

导入播放器：LX Music →「设置 → 自定义源 → 导入」填

```
http://<你的地址>:1101/api/v0/lx-script.js
```

```sh
# 搜索（pt = kw / kg / mg / wy / tx）
curl -s 'http://127.0.0.1:1101/api/v1/search?pt=kw&q=%E5%91%A8%E6%9D%B0%E4%BC%A6&sz=3'

# 取播放直链（id 来自上一步）
curl -s 'http://127.0.0.1:1101/api/v0/url/kw/321946135/320k'

# 让客户端直接跳转
curl -i 'http://127.0.0.1:1101/api/v0/url/kw/321946135/320k?jmp'
```

---

## 能力矩阵（2026-11-20 实测）

| 平台 | 搜索 | 歌词 | 播放直链 | 匿名天花板 | 打开完整音质 |
|---|---|---|---|---|---|
| **酷我** kw | ✅ | ❌ | **✅** | 128k mp3（`anti.s` 忽略音质参数） | — |
| **酷狗** kg | ✅ | **✅** | **✅** | 128k（v5 签名；高音质按曲目权限降档） | 24bit 需该曲有 `ResFileHash` |
| **咪咕** mg | ✅ | **✅** | **✅** | PQ/HQ（App `listenSong` 302） | 无损/24bit 需登录态 |
| **网易云** wy | ✅ | **✅** 含翻译/逐字 | **✅** | 128k（weapi 匿名） | 320k/无损需 `[Wy] Cookie` |
| **QQ** tx | ✅ | **✅**（含翻译） | **✅** | 免费曲目 128k；付费曲目给**试听片段** | 320k/无损/24bit 需 `[Tx] Uin/Ukey` |

实测依据（全部为真实请求，详见 CHANGELOG）：

- 网易云官方 `eapi` 匿名只回 `url:null`，**weapi**（网页版老接口）对免费曲目仍返回真实地址；
- 酷狗 `trackercdn` 的 v5 接口带客户端签名（MD5 双重签名 + `Kg-Thash` 头）才给链接，
  每档音质对应**不同 hash**（24bit 在 `ResFileHash`）；
- QQ 的关键是 `musics.fcg` 的 `sign` 参数（zzb 算法），匿名时用内置占位 QQ 号；
- 咪咕要走 App 的 `listenSong.do`（302 到真实文件），音频商城的 web 接口早已下线；
- 酷我 `anti.s` **忽略 br 参数**，固定返回同一份 mp3，所以只上报保守音质——**宁可不报无损，也不谎报**。

### 音质降级策略

所有源统一「**只降不升**」：请求 `flac24bit` → `flac` → `320k` → `128k` 逐档尝试，
最终上报**实际拿到的档位**（响应里的 `br` / 头信息），拿不到就如实报错，绝不拼接猜测地址。

---

## 接口

### v0 契约

```
GET /api/v0/{method}/{source}/{id}/{quality}
```

| method | 别名 | 说明 |
|---|---|---|
| `url` | `link` | 播放直链 |
| `lrc` | `lyric` | 歌词 |
| `mov` | `mv` | 视频直链 |
| `mid` | — | 音乐信息 |
| `vid` | — | 视频信息 |
| `pic` | — | 封面 |

音质留空时保留尾部斜杠：`/api/v0/url/kw/123/`

查询参数：

- **`jmp`** — `url`/`mov`/`pic` 返回 302；`lrc` 直接吐纯文本；**出错时不跳转，仍返回 JSON**
- **`key`** — 接口密钥（无法设置请求头时使用）

### v1 与其它

```
GET /                                   # 自检页（ApiOnly=true 时 404）
GET /api/v0/ping                        # 健康检查
GET /api/v0/lx-script.js                # 自定义源脚本（?key= 注入密钥，?raw 下载）
GET /api/v1/search?pt=&q=&pg=&sz=       # 搜索
GET /api/v1/file/{source}/{id}/{name}   # 缓存文件服务（ETag / Range / 304 / 206）
GET /api/v1/status                      # 运行状态（含缓存命中率、订阅数）
GET /api/v1/test/sse                    # SSE 日志流（需开启 Apis.WebLog）
```

### 错误码

| 码 | HTTP | 含义 |
|---|---|---|
| 0 | 200 | 成功 |
| 1 | 403 | 无权限 |
| 2 | 503 | 服务不可用（源停用、上游取不到都归这里） |
| 3 | 401 | 密钥缺失或错误 |
| 4 | 500 | 内部错误（未知错误码也归这里） |
| 5 | 429 | 触发限流 |
| 6 | 400 | 参数错误 |
| 7 | 404 | 资源不存在 |

取链失败时如果配了 `[Errs].ErrMp3`，会按 v0 约定 **302 到错误音频**而不是返回 JSON 错误。

---

## 配置

两份文件，职责分离。

### `data/bootstrap.toml` — 启动期，改了要重启

| 键 | 默认 | 说明 |
|---|---|---|
| `Listen` | `:1101` | 监听地址 |
| `Console` | `true` | 是否输出到控制台 |
| `LogDir` | `data/log` | 日志落盘目录（与控制台同时写） |
| `Debug` | `false` | 调试日志 |
| `Cors` | `false` | 加跨域响应头（含 OPTIONS 预检 204） |
| `Proxy` | `false` | 是否信任转发头（`X-Forwarded-*`） |
| `RealIP` | `''` | 自定义真实 IP 头，`|` 分隔 |
| `ReloadInterval` | `2s` | 动态配置轮询间隔 |
| `CacheDir` | `data/cache` | 缓存目录 |
| `DNSServer` | `''` | 自定义 DNS；留空自动判断（Android/Termux 会回落 `223.5.5.5:53`） |

```toml
[Kw]
Strategy = 'anti'      # anti 免密钥 / mobi 需 8 字节 DES 密钥
BaseURL = ''           # 留空用默认
Key = ''               # mobi 策略的 DES 密钥

[Wy]
Agg    = false         # 是否借第三方签名服务取网易云直链（默认关，见下）
AggURL = ''            # 留空用内置默认（GD 音乐台）
Cookie = ''            # 形如 MUSIC_U=xxx；填了官方接口就能给 320k/无损

[Tx]
Cookie = ''            # uin=o0123456789; qqmusic_key=xxx
Uin    = ''            # 也可以只填这两项
Ukey   = ''            # 完整的 320k/无损/24bit 需要它
```

> **关于 `[Wy] Agg`**：网易云官方接口匿名只给 128k，拿更高音质有两条路——
> ① 填 `Cookie`（推荐，无第三方依赖）；② 借第三方签名服务（默认关闭，因为公共实例
> 从部分网络访问时通时不通，会平白加几秒延迟）。

### `data/dynamic.toml` — 可热更，保存后 2 秒内生效

| 段 | 键 | 说明 |
|---|---|---|
| `[Auth.BanList]` | `Enable` `Strict` `White` `Black` | IP 黑白名单（支持 CIDR） |
| `[Auth.RateLimit]` | `Enable` `Block` `Globle` `Single` | 固定窗口限流；全局 + 单来源 |
| `[Auth.ApiPass]` | `Enable` `Auto` `Value` | 接口密钥；`Auto` 会把密钥自动写进下发脚本 |
| `[Apis]` | `WebLog` `ApiOnly` | SSE 日志开关；`ApiOnly` 关掉根路径自检页 |
| `[Srcs.<Kw/Kg/Mg/Wy/Tx>]` | `Enable` | **按源启停，热更即生效**（停用的源回 503） |
| `[Data]` | `Mode` `Type` `TTL` `Rule.*` | 缓存模式/TTL/平台与音质规则 |
| `[Errs]` | `Expi` `ErrMp3` | 取链失败时的错误音频兜底 |

**热更不会清空缓存**：只改变后续写入的规则与 TTL，已有数据一个字节都不动。
这不是约定，是设计——重载器只认识配置快照，没有碰缓存的代码路径。

配置项都**真的会生效**：不存在「解析了却没人用」的键（`Cors` `LogDir` `Srcs` `ApiOnly`
`Data.Type` `Errs` 都是 v0.1.6 才补齐的，此前它们确实只是摆设）。

---

## 缓存语义

- **TTL 与直链有效期是两件事**：缓存默认 1 小时，直链可能 30 分钟就过期。
  所以命中缓存后还要校验 `expi`，过期就作废并回源一次——绝不把死链发给客户端。
- **未知音质不缓存**（防止把平台新音质名误当普通音质），**空音质可缓存**（歌词这类没有音质概念的资源）。
- 缓存键会做消毒：非法 id（含 `/`、空格、中文）映射成 md5，不会因为一个奇怪 id 就永远不缓存。
- 同一键的并发未命中会被合并，只打一次上游；不可缓存的资源直接透传。

---

## 开发

```sh
make test      # 单元测试
make race      # 竞态检测
make smoke     # 端到端冒烟：真起服务 + 真 curl
make check     # fmt + vet + race
make help      # 全部命令
```

CI 跑 `go mod verify` → `gofmt`（仅报告）→ `go vet` → `go build` → `go test -race` → 模糊测试，
另有一个独立 workflow 做端到端冒烟。当前 20 个包的覆盖率大多在 70%~90%。

---

## 已知限制

1. **匿名只到各平台的免费档**：酷我/酷狗/QQ 128k、咪咕 PQ/HQ、网易云 128k。
   要无损或 24bit 必须给凭据（见配置表），这是上游的账号体系决定的，不是实现取舍。
2. **歌词接了网易云 / 酷狗 / QQ / 咪咕**（网易云还有翻译与逐字行）；**酷我**的公开歌词入口已失效，未接。
3. **封面 / MV / 音乐详情**未接入（各平台入口都需要签名或登录）。
4. 缓存驱动只有本地文件；限流计数是单机内存（`Limiter` 接口已预留 Redis 位置）。
5. `mobi` 策略（酷我真实音质 + 歌词）需要 8 字节 DES 密钥，仓库不内置任何密钥。

## 许可

[MIT](LICENSE)。本项目仅用于技术研究与学习，请支持正版音乐。
