# lx-source-next

LX Music 自定义源后端，从零重建。

**只依赖一个第三方库**（`go-toml/v2`），其余全部标准库手写：
DES 加解密、zlib、XML、netip、slog、HTTP 服务都走 stdlib。

架构说明见 [ARCHITECTURE.md](ARCHITECTURE.md) · 更新日志见 [CHANGELOG.md](CHANGELOG.md)

---

## 快速开始

```sh
make build          # 或 go build ./cmd/lxsrcs
./lxsrcs -conf data # 监听 :1101
curl 'http://127.0.0.1:1101/api/v0/ping'
```

首次运行会在 `data/` 里**自动生成** `bootstrap.toml` 与 `dynamic.toml`，
无需手动准备。默认配置已经可以搜索和取链（走酷我匿名接口）。

```sh
# 搜索
curl -s 'http://127.0.0.1:1101/api/v1/search?pt=kw&q=%E5%91%A8%E6%9D%B0%E4%BC%A6&sz=3'

# 取播放直链（ID 来自上一步）
curl -s 'http://127.0.0.1:1101/api/v0/url/kw/474678847/320k'

# 让播放器直接跳转
curl -i 'http://127.0.0.1:1101/api/v0/url/kw/474678847/320k?jmp'
```

---

## 能力

| 能力 | 状态 |
|---|---|
| v0 契约 `url`/`link` `lrc`/`lyric` `mov`/`mv` `mid` `vid` `pic` | ✅ |
| `?jmp` 302 跳转（`lrc` 则直接吐纯文本） | ✅ |
| v1 搜索 `/api/v1/search` | ✅ |
| 缓存文件服务 `/api/v1/file/...`（`ETag` / `Range` / 304 / 206） | ✅ |
| SSE 日志流 `/api/v1/test/sse` | ✅ |
| 歌词 LRC / LRCX / KRC / QRC / NRC | ✅ |
| 酷我 kw 搜索 + 直链（匿名） | ✅ |
| 酷狗 kg 搜索（匿名） | ✅ |
| 网易云 wy 搜索 + 歌词（匿名） | ✅ |
| 酷狗/网易云 直链 | ❌ 酷狗无直链、网易云需加密 |
| 酷我歌词 / 封面 / MV | ❌ 需凭据 |
| 咪咕 mg | ❌ 接口返回 HTML |

### 关于酷我

实测结论（详见 ARCHITECTURE.md 第 12 节）：`antiserver.kuwo.cn` 与 `search.kuwo.cn`
可匿名使用；歌词/封面/MV 的公开入口全部失效或需要 csrf。

⚠️ `anti.s` **会忽略 `br` 参数**，固定返回同一个 mp3。因此 anti 策略一律上报
保守音质（默认 `128k`，可用 `Kw.AntiQuality` 调整）——**宁可低报，绝不把 mp3 谎称无损**。

---

## 接口

### 导入播放器

```
GET /api/v0/lx-script.js        # 自定义源脚本（?key= 注入密钥，?raw 下载）
GET /api/v0/script              # 同上，短别名
```

在 LX Music 里「设置 → 自定义源 → 导入」填入
`http://<你的地址>:1101/api/v0/lx-script.js` 即可。

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

- **`jmp`** — `url`/`mov`/`pic` 返回 302；`lrc` 返回纯文本；**出错时不跳转，仍返回 JSON**
- **`key`** — 接口密钥（无法设置请求头时使用）

### v1

```
GET /api/v1/search?pt=kw&q=关键词&pg=1&sz=10
GET /api/v1/file/{source}/{musicid}/{quality}.{ext}
GET /api/v1/status
GET /api/v1/test/sse?token=密钥
```

### 错误码

| 码 | HTTP | 含义 |
|---|---|---|
| 0 | 200 | 成功 |
| 1 | 403 | 无权限 |
| 2 | 503 | 服务不可用 |
| 3 | 401 | 密钥缺失或错误 |
| 4 | 500 | 内部错误 |
| 5 | 429 | 触发限流 |
| 6 | 400 | 参数错误 |
| 7 | 404 | 资源不存在 |

---

## 配置

两份文件，职责分离：

`data/bootstrap.toml` — **启动期**，改了要重启

```toml
Listen = ':1101'
Proxy = false            # 是否信任转发头
ReloadInterval = '2s'    # 动态配置轮询间隔
CacheDir = 'data/cache'

[Kw]
Strategy = 'anti'        # anti 免密钥 / mobi 需 DES 密钥
BaseURL = ''             # 留空用默认
Key = ''                 # mobi 策略的 8 字节密钥
```

`data/dynamic.toml` — **可热更**，保存后自动生效

```toml
[Auth.ApiPass]
Enable = false
Value = ''

[Auth.RateLimit]
Enable = false
Single = 15              # 单来源每窗口上限

[Data]
Mode = 1                 # 0 禁用 / 1 安全 / 2 极速
TTL = 3600
```

**热更不会清空缓存**：只改变后续写入的规则与 TTL，已有数据一个字节都不动。
这不是约定，是设计——重载器只认识配置快照，没有碰缓存的代码路径。

---

## 开发

```sh
make test      # 单元测试
make race      # 竞态检测
make smoke     # 端到端冒烟：真起服务 + 真 curl
make check     # fmt + vet + race
make help      # 全部命令
```

CI 跑 `gofmt` / `go vet` / `go build` / `go test -race` / 模糊测试，
外加一个独立 workflow 做端到端冒烟。

---

## 已知限制

1. **只有酷我一个平台**，且匿名能力限于「搜索 + mp3 取链」。
2. **歌词接口全部失效**，LX 客户端的歌词会退回它自己的内置源。
3. `mobi` 策略（真实音质 + 歌词）需要 DES 密钥，本仓库不内置任何密钥。
4. 缓存驱动只有本地文件；`Limiter` 接口已预留 Redis 实现位置。

## 许可

[MIT](LICENSE)。本项目仅用于技术研究与学习，请支持正版音乐。
