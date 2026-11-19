#!/usr/bin/env bash
# 端到端冒烟：起真实服务，用真实 curl 打完整链路。
set -uo pipefail

PORT="${PORT:-1101}"
BASE="http://127.0.0.1:${PORT}"
BIN="./lxsrcs-smoke"

line() { printf '\n────────────────────────────────────────────────\n'; }

show() {
  local title="$1"; shift
  line
  printf '### %s\n' "$title"
  printf '$ curl %s\n' "$*"
  curl -sS -i --max-time 5 "$@" 2>&1 || printf '[curl 调用失败]\n'
  printf '\n'
}

echo "== 准备环境 =="
rm -rf data/cache data/log
mkdir -p data/cache data/log
go build -o "$BIN" ./cmd/lxsrcs || { echo "构建失败"; exit 1; }

echo "== 启动服务 =="
"$BIN" -conf data > /tmp/lxsrcs.log 2>&1 &
PID=$!
trap 'kill $PID 2>/dev/null' EXIT

i=0
READY=0
while [ "$i" -lt 60 ]; do
  if curl -sf --max-time 1 "$BASE/api/v0/ping" >/dev/null 2>&1; then
    READY=1
    break
  fi
  i=$((i + 1))
  sleep 0.25
done
if [ "$READY" != "1" ]; then
  echo "服务未能启动，日志如下："
  cat /tmp/lxsrcs.log
  exit 1
fi
echo "服务已就绪 (pid=$PID)"

echo "== 启动日志 =="
head -20 /tmp/lxsrcs.log

show "健康检查" "$BASE/api/v0/ping"
show "v0 取链" "$BASE/api/v0/url/mock/123/320k"
show "v0 别名 link" "$BASE/api/v0/link/mock/123/320k"
show "v0 音质回落 master → flac" "$BASE/api/v0/url/mock/123/master"
show "v0 音质留空（尾部斜杠）" "$BASE/api/v0/url/mock/123/"
show "jmp 302 播放直链" "$BASE/api/v0/url/mock/123/320k?jmp"
show "jmp 302 视频" "$BASE/api/v0/mv/mock/5397146/720p?jmp"
show "jmp 302 封面" "$BASE/api/v0/pic/mock/123/m?jmp"
show "jmp lrc 纯文本" "$BASE/api/v0/lrc/mock/123?jmp"
show "音乐信息 mid" "$BASE/api/v0/mid/mock/1912598037"
show "视频信息 vid" "$BASE/api/v0/vid/mock/90001"
show "视频直链 mov" "$BASE/api/v0/mov/mock/5397146/720p"

show "错误：未知方法 → 400" "$BASE/api/v0/nope/mock/123/320k"
show "错误：未知来源 → 404" "$BASE/api/v0/url/xx/123/320k"
show "错误：资源不存在 → 404" "$BASE/api/v0/url/mock/notfound/320k"
show "错误：jmp 失败仍返回 JSON" "$BASE/api/v0/url/mock/notfound/320k?jmp"

line
printf '### 缓存链路：连打 3 次再查统计\n'
curl -sS "$BASE/api/v0/url/mock/cache-check/320k" >/dev/null
curl -sS "$BASE/api/v0/url/mock/cache-check/320k" >/dev/null
curl -sS "$BASE/api/v0/url/mock/cache-check/320k" >/dev/null
curl -sS -i "$BASE/api/v1/status"
printf '\n### 缓存落盘内容\n'
find data/cache -type f | sort

line
printf '### 文件路由：放一个文件进缓存目录\n'
mkdir -p data/cache/wy/1
printf '0123456789abcdefghijklmnopqrstuvwxyz' > data/cache/wy/1/320k.mp3
show "文件元信息（HEAD）" -I "$BASE/api/v1/file/wy/1/320k.mp3"
show "Range 分片 0-9" -r 0-9 "$BASE/api/v1/file/wy/1/320k.mp3"
show "文件不存在 → 404" "$BASE/api/v1/file/wy/1/none.mp3"
show "路径穿越 → 拒绝" "$BASE/api/v1/file/wy/%2e%2e/320k.mp3"
show "内部键隔离 → 404" "$BASE/api/v1/file/_meta/mock/123-320k.url"

line
printf '### 热更鉴权：打开 ApiPass，不重启即生效\n'
cp data/dynamic.toml /tmp/dynamic.bak
# 只改 [Auth.ApiPass] 段内的键，避免误伤其它开关
sed -i "/^\[Auth.ApiPass\]/,/^\[/ s/^Enable = false$/Enable = true/" data/dynamic.toml
sed -i "/^\[Auth.ApiPass\]/,/^\[/ s/^Value = ''$/Value = 'secret'/" data/dynamic.toml
sleep 3
sed -n '/ApiPass/,+4p' data/dynamic.toml
show "缺少密钥 → 401" "$BASE/api/v0/url/mock/123/320k"
show "带密钥头 → 200" -H 'X-Request-Key: secret' "$BASE/api/v0/url/mock/123/320k"
show "查询参数密钥 → 200" "$BASE/api/v0/url/mock/123/320k?key=secret"
cp /tmp/dynamic.bak data/dynamic.toml

line
printf '### 限流：Single 调到 2，连打 4 次\n'
# 同样只改 [Auth.RateLimit] 段内的键
sed -i "/^\[Auth.RateLimit\]/,/^\[/ s/^Enable = false$/Enable = true/" data/dynamic.toml
sed -i "/^\[Auth.RateLimit\]/,/^\[/ s/^Single = 15$/Single = 2/" data/dynamic.toml
sleep 3
for n in 1 2 3 4; do
  printf '第 %s 次 → ' "$n"
  curl -sS -o /dev/null -w 'HTTP %{http_code}\n' "$BASE/api/v0/url/mock/rate/320k"
done
# 恢复配置并等热更生效，否则后续用例会被限流器挡住
cp /tmp/dynamic.bak data/dynamic.toml
sleep 3
sed -n '/Auth.RateLimit/,+4p' data/dynamic.toml
curl -sS "$BASE/api/v0/ping" >/dev/null

line
printf '### 酷我来源（anti 策略：免密钥，真实网络）\n'
show "kw 取链（可能因网络/风控失败，失败即打印上一行）" "$BASE/api/v0/url/kw/228908/320k"
show "kw 音质回落 master" "$BASE/api/v0/url/kw/228908/master"
show "kw 未实现能力 → 503" "$BASE/api/v0/mid/kw/228908"

line
printf '### 诊断：br 参数是否真的生效（同曲不同音质对比）\n'
for q in 128k 320k flac flac24bit; do
  printf '%-10s → ' "$q"
  curl -sS "$BASE/api/v0/url/kw/228908/$q"
  printf '\n'
done

line
printf '### 服务搜索（真实网络，匿名可用）\n'
show "搜索 周杰伦" "$BASE/api/v1/search?pt=kw&q=%E5%91%A8%E6%9D%B0%E4%BC%A6&sz=3"
show "搜索缺少关键词 → 400" "$BASE/api/v1/search?pt=kw"
show "搜索不支持的来源 → 503" "$BASE/api/v1/search?pt=mock&q=x"

line
printf '### 自定义源脚本（可直接导入 LX Music）\n'
show "脚本头部" "$BASE/api/v0/lx-script.js" | head -30
echo "--- 注入的地址与源 ---"
curl -sS "$BASE/api/v0/lx-script.js" | grep -E "API_ADDR|kw:|sources\[|__" | head -8
echo "--- 带密钥注入 ---"
curl -sS "$BASE/api/v0/lx-script.js?key=smoke-key" | grep -E "API_KEY" | head -2

line
printf '### 酷狗 kg（公开接口，真实网络）\n'
show "kg 搜索" "$BASE/api/v1/search?pt=kg&q=%E5%91%A8%E6%9D%B0%E4%BC%A6&sz=2"
show "kg 取链（用实测拿到的 hash）" "$BASE/api/v0/url/kg/1f7e7659b9fff4fb6f71562a61bbc25f/128k"
show "kg 歌词（未接入，应 503）" "$BASE/api/v0/lrc/kg/1f7e7659b9fff4fb6f71562a61bbc25f"

line
printf '### 网易云 wy（公开接口，真实网络）\n'
show "wy 搜索" "$BASE/api/v1/search?pt=wy&q=%E5%91%A8%E6%9D%B0%E4%BC%A6&sz=2"
show "wy 歌词" "$BASE/api/v0/lrc/wy/347230"
show "wy 取链（需加密，应 503）" "$BASE/api/v0/url/wy/347230/320k"

line
printf '### QQ 音乐 tx（vkey 流程）\n'
show "tx 搜索" "$BASE/api/v1/search?pt=tx&q=%E5%91%A8%E6%9D%B0%E4%BC%A6&sz=2"
show "tx 取链" "$BASE/api/v0/url/tx/0027oMO61wWi55/128k"

line
printf '### 网易云 wy（eapi 取链）\n'
show "wy 取链" "$BASE/api/v0/url/wy/347230/320k"

line
printf '### 逐首实验：搜索出来的歌到底有几首能放\n'
for pt in wy tx; do
  printf '\n[%s] 搜索后逐个试取链：\n' "$pt"
  curl -sS "$BASE/api/v1/search?pt=$pt&q=%E5%91%A8%E6%9D%B0%E4%BC%A6&sz=4" -o /tmp/s_$pt.json 2>/dev/null
  grep -oE '"id":"[0-9A-Za-z]+"' /tmp/s_$pt.json | sed -E 's/.*:"([^"]+)"/\1/' > /tmp/ids_$pt.txt
  n=0
  while read -r ID; do
    [ -z "$ID" ] && continue
    n=$((n + 1))
    printf '  %d) %-20s → ' "$n" "$ID"
    curl -sS -o /dev/null -w 'HTTP %{http_code}\n' "$BASE/api/v0/url/$pt/$ID/128k"
  done < /tmp/ids_$pt.txt
done

line
printf '### 服务端日志尾部\n'
tail -25 /tmp/lxsrcs.log

line
echo "冒烟结束"
