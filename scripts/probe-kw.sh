#!/usr/bin/env bash
# 探测酷我公开匿名接口，用于决定接入哪一条。
# 只做少量 GET 请求，打印状态码与响应片段。
set -uo pipefail

probe() {
  local url="$1"
  printf '\n─────────────\n%s\n' "$url"
  curl -sS -m 10 -o /tmp/probe.out -w 'HTTP %{http_code} | type=%{content_type} | %{size_download} bytes | %{time_total}s\n' "$url" 2>&1
  head -c 320 /tmp/probe.out 2>/dev/null
  printf '\n'
}

echo "=== 1) H5 歌曲信息 + 歌词（疑似公开） ==="
probe 'https://m.kuwo.cn/newh5/singles/songinfoandlrc?musicId=228908'

echo "=== 2) 老版 mobi 免密钥取链（web 客户端标识） ==="
probe 'https://mobi.kuwo.cn/mobi.s?f=web&source=kwplayer_ar_5.1.0.0_B_jiakong_vh.apk&type=convert_url&br=320kmp3&rid=228908'

echo "=== 3) anti.s 请求 flac 格式（看是否会拒绝或换文件） ==="
probe 'http://antiserver.kuwo.cn/anti.s?type=convert_url&format=flac&response=url&rid=MUSIC_228908'

echo "=== 4) anti.s 请求 mp4（MV 路径探路） ==="
probe 'http://antiserver.kuwo.cn/anti.s?type=convert_url&format=mp4&response=url&rid=MV_90001'

echo "=== 5) 网页版 musicInfo（多半需要 csrf/cookie） ==="
probe 'https://www.kuwo.cn/api/www/music/musicInfo?mid=228908&httpsStatus=1'

echo "=== 6) 歌手图接口（封面路径探路） ==="
probe 'http://artistpicserver.kuwo.cn/pic.yy.protocol?name=228908&size=500'

echo
echo "=== 探测结束 ==="
