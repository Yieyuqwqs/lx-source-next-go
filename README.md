# lx-source-next

LX Music 自定义源后端，重构版本。

## 当前状态

| 模块 | 状态 |
|---|---|
| `pkg/lrcutil` 标准 LRC 解析/序列化 | ✅ |
| `pkg/lrcutil` LX 逐字歌词（lxlyric） | ✅ |
| `pkg/lrcutil` KRC（酷狗）解密 + 解析 | ✅ |
| `pkg/lrcutil` QRC（QQ）三重 DES 解密 + XML 解析 | ✅ |
| `pkg/lrcutil` NRC（网易）逐字解析 | ✅ |
| `pkg/lrcutil` 逐字 → 普通 LRC 降级 | ✅ |
| `pkg/apierr` 错误码统一映射 | ⏳ |
| `pkg/quality` 音质回落统一表 | ⏳ |
| `pkg/balancer` 账号池熔断 | ⏳ |
| `pkg/ipguard` netip 黑白名单 | ⏳ |
| slog → SSE Handler | ⏳ |
| 各平台上游协议层 | ❌ 未开始 |

## 设计原则

1. **纯函数优先**：`pkg/*` 下的解析、解密、转换一律无状态、无网络 IO、无全局变量。
2. **只依赖标准库**：`pkg/lrcutil` 零第三方依赖（DES、zlib、XML 全部用 stdlib）。
3. **时间统一毫秒 `int64`**；行内词时间统一归一化为**相对行首**，消除各格式差异。
4. **不猜**：格式有歧义时不自动应用（如 LRC 的 `[offset:]`），交给调用方决定。

## 已支持的歌词格式

| 格式 | 容器/加密 | 明文形状 |
|---|---|---|
| LRC | 无 | `[分:秒.百分秒]文本` |
| LX 逐字 | 无 | `[分:秒.毫秒]<相对起,时长>文字` |
| KRC（酷狗） | `krc1` 魔数 + 16 字节循环 XOR + zlib | `[行起,行时]<相对词起,词时,0>文字` |
| QRC（QQ） | 可选 `0x98 0x25` 转旧版 → 跳过 11 字节头 → DES 解/加/解（ECB，密钥取前 8 字节）→ zlib | XML，`Lyric_*` 的 `LyricContent` 内为 `[行起,行时]文字(绝对词起,词时)` |
| NRC（网易） | 无 | `[行起,行时](绝对词起,词时[,0])文字` |

## 验证方式

```sh
gofmt -l .
go vet ./...
go test ./... -race
go test ./pkg/lrcutil -run '^$' -fuzz '^FuzzParseLrcx$' -fuzztime 30s
```

测试里的 KRC / QRC 二进制 fixture 由**参考实现的常量生成**，
再交给本包的 `Decode*` / `Parse*` 还原，属跨实现校验，不是自说自话的往返测试。

推送到 GitHub 后 `.github/workflows/ci.yml` 自动执行上述检查。
