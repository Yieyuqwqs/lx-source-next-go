#!/usr/bin/env bash
# 第四轮：只攻歌词，测试是否因为缺少 UA/Referer 被拒。
set -uo pipefail

ID=474678847
UA_M='Mozilla/5.0 (Linux; Android 13; MI 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Mobile Safari/537.36'

p() {
  local title="$1"; shift
  printf '\n─────────────\n%s\n' "$title"
  curl -sS -m 12 -o /tmp/l.out -w 'HTTP %{http_code} | %{size_download} bytes\n' "$@" 2>&1
  head -c 300 /tmp/l.out 2>/dev/null
  printf '\n'
}

p "1) H5 歌词 + 移动端 UA" \
  -H "User-Agent: $UA_M" \
  "https://m.kuwo.cn/newh5/singles/songinfoandlrc?musicId=$ID"

p "2) H5 歌词 + UA + Referer" \
  -H "User-Agent: $UA_M" -H "Referer: https://m.kuwo.cn/" \
  "https://m.kuwo.cn/newh5/singles/songinfoandlrc?musicId=$ID"

p "3) H5 歌词 + httpsStatus" \
  -H "User-Agent: $UA_M" \
  "https://m.kuwo.cn/newh5/singles/songinfoandlrc?musicId=$ID&httpsStatus=1"

p "4) 老版 newlyric 纯文本 + UA" \
  -H "User-Agent: $UA_M" \
  "http://newlyric.kuwo.cn/newlyric.lrc?$ID"

p "5) 网页版歌词 API + UA" \
  -H "User-Agent: $UA_M" -H "Referer: https://www.kuwo.cn/" \
  "https://www.kuwo.cn/api/www/music/lyric?musicId=$ID&httpsStatus=1"

echo
echo "=== 第四轮结束 ==="
