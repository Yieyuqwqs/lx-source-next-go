#!/usr/bin/env bash
# 单个 id 的取链体检：接口回了什么、最终文件到底能不能下载。
#
# 用法：
#   BASE=http://127.0.0.1:1101 bash scripts/diag.sh kg 1f7e7659b9fff4fb6f71562a61bbc25f flac24bit
#   bash scripts/diag.sh            # 不给参数就吐用法
#
# 为什么要它：播放器只会说「换源失败」，分不清是
#   ① 我们接口 4xx/5xx（上游拿不到）
#   ② 我们接口 200，但给的直链下不动（CDN 403 / 过期 / 不是音频）
# 这个脚本把两段都打出来，照着就能判断该修哪边。
set -uo pipefail

BASE="${BASE:-http://127.0.0.1:1101}"
KEY="${KEY:-}"
PT="${1:-}"
ID="${2:-}"
Q="${3:-128k}"

if [ -z "$PT" ] || [ -z "$ID" ]; then
  sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi

HDR=()
[ -n "$KEY" ] && HDR=(-H "X-Request-Key: $KEY")

line() { printf '\n─────────────\n%s\n' "$1"; }

line "1) 取链接口：/api/v0/url/$PT/$ID/$Q"
BODY=$(curl -sS -m 20 "${HDR[@]}" "$BASE/api/v0/url/$PT/$ID/$Q" 2>&1)
printf '%s\n' "$BODY" | head -c 600
printf '\n'

# 抽出 data 里的链接与 ext.qual（不依赖 jq）
LINK=$(printf '%s' "$BODY" | sed -n 's/.*"data":"\([^"]*\)".*/\1/p')
QUAL=$(printf '%s' "$BODY" | sed -n 's/.*"qual":"\([^"]*\)".*/\1/p')
CODE=$(printf '%s' "$BODY" | sed -n 's/.*"code":\([0-9-]*\).*/\1/p')
MSG=$(printf '%s' "$BODY" | sed -n 's/.*"msg":"\([^"]*\)".*/\1/p')

line "2) 解析结果"
printf 'code = %s\nmsg  = %s\nqual = %s\nlink = %s\n' "${CODE:-?}" "${MSG:-}" "${QUAL:-}" "${LINK:-<无>}"

if [ -z "$LINK" ]; then
  printf '\n接口没给出直链：问题在「取链」这一侧，见上面的 msg。\n'
else
  line "3) 直链能否真的下到（Range 0-1023）"
  curl -sS -m 20 -o /tmp/diag.bin -D /tmp/diag.hdr \
    -H 'User-Agent: Mozilla/5.0' -H 'Range: bytes=0-1023' "$LINK" 2>&1
  grep -iE '^(HTTP/|content-type|content-length|content-range|location)' /tmp/diag.hdr 2>/dev/null | sed 's/\r$//'
  printf '收到 %s 字节\n' "$(wc -c < /tmp/diag.bin 2>/dev/null || echo 0)"
  printf '头 4 字节（mp3 应以 fffb/4944 开头）: '
  od -An -tx1 -N4 /tmp/diag.bin 2>/dev/null | tr -d '\n'; printf '\n'
  printf '\n直链下不动 → 问题在 CDN（过期/风控/Referer），不是取链。\n'
fi

line "4) 顺带看一眼歌词：/api/v0/lrc/$PT/$ID"
curl -sS -m 20 "${HDR[@]}" "$BASE/api/v0/lrc/$PT/$ID" | head -c 200
printf '\n'
