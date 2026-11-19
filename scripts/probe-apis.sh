#!/usr/bin/env bash
# 探测第三方公开音乐 API，看哪些还活着、返回什么。
set -uo pipefail

UA='Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'

p() {
  local title="$1"; shift
  printf '\n─────────────\n%s\n' "$title"
  curl -sS -m 12 -A "$UA" -o /tmp/api.out \
    -w 'HTTP %{http_code} | %{content_type} | %{size_download}B | %{time_total}s\n' "$@" 2>&1
  head -c 260 /tmp/api.out 2>/dev/null
  printf '\n'
}

echo "=== 网易云：搜索（匿名） ==="
p "search/get/web" -e 'https://music.163.com/' \
  'https://music.163.com/api/search/get/web?csrf_token=&s=%E5%91%A8%E6%9D%B0%E4%BC%A6&type=1&offset=0&total=true&limit=3'

echo "=== 网易云：歌词（匿名） ==="
p "song/lyric" -e 'https://music.163.com/' \
  'https://music.163.com/api/song/lyric?id=347230&lv=-1&kv=-1&tv=-1'

echo "=== 网易云：播放直链（匿名，多半要加密） ==="
p "enhance/player/url" -e 'https://music.163.com/' \
  'https://music.163.com/api/song/enhance/player/url?ids=%5B347230%5D&br=320000'

echo "=== 酷狗：搜索（匿名） ==="
p "mobilecdn.kugou.com search" \
  'http://mobilecdn.kugou.com/api/v3/search/song?format=json&keyword=%E5%91%A8%E6%9D%B0%E4%BC%A6&page=1&pagesize=3'

echo "=== 酷狗：歌曲信息/直链（匿名，需 hash） ==="
p "m.kugou.com getSongInfo" \
  'http://m.kugou.com/app/i/getSongInfo.php?cmd=playInfo&hash='

echo "=== 咪咕：搜索（匿名） ==="
p "migu scr_search_tag" \
  'https://m.music.migu.cn/migu/remoting/scr_search_tag?keyword=%E5%91%A8%E6%9D%B0%E4%BC%A6&type=2&rows=3&pgc=1'

echo "=== QQ音乐：搜索（匿名，需 referer） ==="
p "c.y.qq.com client_search_cp" -e 'https://y.qq.com/' \
  'https://c.y.qq.com/soso/fcgi-bin/client_search_cp?w=%E5%91%A8%E6%9D%B0%E4%BC%A6&format=json&n=3'

echo
echo "=== 探测结束 ==="
