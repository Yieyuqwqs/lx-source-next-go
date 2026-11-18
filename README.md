# lx-source-next

LX Music 自定义源后端，重构版本。架构说明见 [ARCHITECTURE.md](ARCHITECTURE.md)。

## 模块

| 包 | 职责 |
|---|---|
| `cmd/lxsrcs` | 组合根：唯一创建具体实现并接线的地方 |
| `pkg/server` | 装配中间件链与路由，唯一知道装配顺序的地方 |
| `pkg/config` | bootstrap/dynamic 分层、原子快照、幂等热重载 |
| `pkg/middleware` | 标准 `func(http.Handler) http.Handler` 组合原语 |
| `pkg/middleware/access` | IP 黑白名单（可插拔） |
| `pkg/middleware/apikey` | 接口密钥（可插拔） |
| `pkg/middleware/ratelimit` | 限流，`Limiter` 接口可换 Redis（可插拔） |
| `pkg/middleware/recoverer` | panic → 500 |
| `pkg/middleware/reqlog` | 访问日志 |
| `pkg/httpx` | 响应信封、错误落盘、客户端 IP |
| `pkg/apierr` | 错误码唯一真源 |
| `pkg/ipguard` | `netip.Prefix` 黑白名单 |
| `pkg/quality` | 音质别名归一化 + 回落链 |
| `pkg/balancer` | 账号池熔断 |
| `pkg/logx` | `slog` Handler → SSE 扇出 + 凭据脱敏 |
| `pkg/lrcutil` | LRC / LRCX / KRC / QRC / NRC 五格式 |

## 运行

```sh
go build ./...
go run ./cmd/lxsrcs -conf data
curl 'http://127.0.0.1:1101/api/v0/ping'
```

配置分两份：

- `data/bootstrap.toml`：监听地址、日志、代理等，改了要重启；
- `data/dynamic.toml`：鉴权、源、缓存规则等，保存后 2 秒内自动生效，**不影响缓存**。

## 验证

```sh
gofmt -l .
go mod verify
go vet ./...
go build ./...
go test ./... -race
go test ./pkg/lrcutil -run '^$' -fuzz '^FuzzParseLrcx$' -fuzztime 30s
```

歌词的 KRC/QRC 二进制 fixture 由参考实现的常量生成，属跨实现校验。

## 尚未实现

- 各平台上游协议层
- 缓存驱动
- 前端静态资源挂载
