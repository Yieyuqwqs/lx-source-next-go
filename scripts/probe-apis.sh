#!/usr/bin/env bash
# 第二轮：把「搜索可用」的方案继续打通到「能拿直链」。
set -uo pipefail
UA='Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'

p() {
  local title="$1"; shift
  printf '\n─────────────\n%s\n' "$title"
  curl -sS -m 12 -A "$UA" -o /tmp/api.out -w 'HTTP %{http_code} | %{content_type} | %{size_download}B\n' "$@" 2>&1
  head -c 300 /tmp/api.out 2>/dev/null
  printf '\n'
}

echo "=== A) 酷狗搜索 → 取 hash ==="
curl -sS -m 12 -A "$UA" -o /tmp/kg.json \
  'http://mobilecdn.kugou.com/api/v3/search/song?format=json&keyword=%E5%91%A8%E6%9D%B0%E4%BC%A6&page=1&pagesize=3' 2>&1
grep -oE '"hash":"[0-9a-f]{32}"' /tmp/kg.json | head -3 > /tmp/hashes.txt
head -3 /tmp/hashes.txt
sed -E 's/.*"hash":"([0-9a-f]{32})".*/\1/' /tmp/hashes.txt > /tmp/hash1.txt
KG_HASH=$(head -1 /tmp/hash1.txt)
printf '取到 hash = [%s]\n' "$KG_HASH"

echo "=== B) 酷狗：用真实 hash 取直链 ==="
if [ -n "$KG_HASH" ]; then
  p "getSongInfo" "http://m.kugou.com/app/i/getSongInfo.php?cmd=playInfo&hash=$KG_HASH"
fi

echo "=== C) 咪咕：跟随重定向 ==="
p "migu -L" -L 'https://m.music.migu.cn/migu/remoting/scr_search_tag?keyword=%E5%91%A8%E6%9D%B0%E4%BC%A6&type=2&rows=3&pgc=1'

echo "=== D) 网易云：另一条搜索入口 ==="
p "search/get" -e 'https://music.163.com/' \
  'https://music.163.com/api/search/get?s=%E5%91%A8%E6%9D%B0%E4%BC%A6&type=1&limit=3&offset=0'

echo "=== E) 网易云歌词：看翻译/逐字字段是否存在 ==="
curl -sS -m 12 -A "$UA" -e 'https://music.163.com/' -o /tmp/wy.json \
  'https://music.163.com/api/song/lyric?id=347230&lv=-1&kv=-1&tv=-1&yv=-1' 2>&1
grep -oE '"(lrc|tlyric|klyric|yrc|romalrc)":' /tmp/wy.json | sort -u
printf 'lrc 片段: '
head -c 160 /tmp/wy.json

echo
echo "=== 第二轮结束 ==="
