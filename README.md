# lx-source-next

LX Music 自定义源后端，重构版本。

## 当前状态

| 模块 | 状态 |
|---|---|
| `pkg/lrcutil` 标准 LRC 解析/序列化 | ✅ 已实现，13 个测试 + 模糊测试 |
| `pkg/lrcutil` KRC / QRC / YRC 逐字格式 | ⏳ 待实现（需要真实样本校验） |
| `pkg/apierr` 错误码统一映射 | ⏳ 待实现 |
| `pkg/quality` 音质回落统一表 | ⏳ 待实现 |
| `pkg/balancer` 账号池熔断 | ⏳ 待实现 |
| `pkg/ipguard` netip 黑白名单 | ⏳ 待实现 |
| 各平台上游协议层 | ❌ 未开始（需要协议资料 + 真实联调） |

## 设计原则

1. **纯函数优先**：`pkg/*` 下的解析、转换、映射逻辑一律无状态、无网络 IO、无全局变量。
2. **只依赖标准库**：`pkg/lrcutil` 零第三方依赖，便于单独测试与复用。
3. **时间统一毫秒 `int64`**，避免各格式时间单位不一致引发的偏移 bug。
4. **不猜**：格式约定有歧义时（如 `[offset:]` 的符号方向），不自动应用，交给调用方决定。

## 本地验证

```sh
gofmt -l .
go vet ./...
go test ./... -race
go test ./pkg/lrcutil -run '^$' -fuzz '^FuzzParse$' -fuzztime 30s
```

推送到 GitHub 后，`.github/workflows/ci.yml` 会自动跑上述检查。

## 已收录的兼容性细节

- `[mm:ss]` / `[mm:ss.xx]` / `[mm:ss.xxx]` / `[hh:mm:ss.xx]`
- 网易云偶发双冒号 `[00:40:59]` 按百分秒处理，等价于 `[00:40.59]`
- 一行多时间戳展开为多行
- 小数位不足右补零（`.5` → 500ms，`.05` → 50ms）
- 输出统一百分秒
