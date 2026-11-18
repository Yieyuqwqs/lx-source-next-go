#!/usr/bin/env bash
# 探测酷我公开匿名接口（第二轮）：聚焦歌词与真实 musicId。
set -uo pipefail

probe() {
  local url="$1"
  printf '\n─────────────\n%s\n' "$url"
  curl -sS -m 10 -o /tmp/probe.out -w 'HTTP %{http_code} | type=%{content_type} | %{size_download} bytes\n' "$url" 2>&1
  head -c 400 /tmp/probe.out 2>/dev/null
  printf '\n'
}

echo "=== A) 用 mp3 资源里的 ID 再试 H5 歌词 ==="
probe 'https://m.kuwo.cn/newh5/singles/songinfoandlrc?musicId=588957081'

echo "=== B) 老版 newlyric 纯文本歌词接口（资源 ID） ==="
probe 'http://newlyric.kuwo.cn/newlyric.lrc?588957081'

echo "=== C) 老版 newlyric 纯文本歌词接口（anti.s 的 rid） ==="
probe 'http://newlyric.kuwo.cn/newlyric.lrc?228908'

echo "=== D) 用资源 ID 再试 anti.s（验证 ID 空间） ==="
probe 'http://antiserver.kuwo.cn/anti.s?type=convert_url&format=mp3&response=url&rid=MUSIC_588957081'

echo "=== E) 歌曲封面（从 ID 推） ==="
probe 'http://kuwo.cn/api/www/music/musicInfo?mid=588957081&httpsStatus=1'

echo
echo "=== 第二轮探测结束 ==="
