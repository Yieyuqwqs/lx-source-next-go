#!/usr/bin/env bash
# 全量探测：重点攻「播放直链」。
set -uo pipefail
UA='Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
KG_HASH='1f7e7659b9fff4fb6f71562a61bbc25f'

p() {
  local title="$1"; shift
  printf '\n─────────────\n%s\n' "$title"
  curl -sS -m 12 -A "$UA" -o /tmp/a.out -w 'HTTP %{http_code} | %{size_download}B | %{content_type}\n' "$@" 2>&1
  head -c 320 /tmp/a.out 2>/dev/null
  printf '\n'
}

echo "############ 一、酷狗取链（trackercdn 系列）############"

KEY2=$(printf '%s' "${KG_HASH}kgcloudv2" | md5sum | cut -d' ' -f1)
printf 'md5(hash+kgcloudv2) = %s\n' "$KEY2"
p "trackercdn v2 hq" -H "Referer: http://m.kugou.com/" \
  "http://trackercdn.kugou.com/i/v2/?key=$KEY2&hash=$KG_HASH&br=hq&appid=1005&pid=2&cmd=25&behavior=play"

p "trackercdn v2 flac" -H "Referer: http://m.kugou.com/" \
  "http://trackercdn.kugou.com/i/v2/?key=$KEY2&hash=$KG_HASH&br=flac&appid=1005&pid=2&cmd=25&behavior=play"

KEY1=$(printf '%s' "${KG_HASH}kgcloud" | md5sum | cut -d' ' -f1)
p "trackercdn v1" -H "Referer: http://m.kugou.com/" \
  "http://trackercdn.kugou.com/i/?cmd=4&pid=1&behavior=play&hash=$KG_HASH&key=$KEY1"

p "getSongInfo 带 from 参数" -H "Referer: http://m.kugou.com/" \
  "http://m.kugou.com/app/i/getSongInfo.php?cmd=playInfo&hash=$KG_HASH&from=mkugou"

echo
echo "############ 二、QQ 音乐 vkey 流程 ############"
curl -sS -m 12 -A "$UA" -e 'https://y.qq.com/' -o /tmp/qq.json \
  'https://c.y.qq.com/soso/fcgi-bin/client_search_cp?w=%E5%91%A8%E6%9D%B0%E4%BC%A6&format=json&n=3' 2>&1
grep -oE '"songmid":"[0-9A-Za-z]+"' /tmp/qq.json | head -3
grep -oE '"songmid":"[0-9A-Za-z]+"' /tmp/qq.json | head -1 | sed -E 's/.*"songmid":"([0-9A-Za-z]+)".*/\1/' > /tmp/mid.txt
QQ_MID=$(head -1 /tmp/mid.txt)
printf '取到 songmid = [%s]\n' "$QQ_MID"

if [ -n "$QQ_MID" ]; then
  p "fcg_music_express_mobile3（拿 vkey）" -e 'https://y.qq.com/' \
    "https://c.y.qq.com/base/fcgi-bin/fcg_music_express_mobile3.fcg?format=json&platform=yqq&cid=205361747&songmid=$QQ_MID&filename=C400$QQ_MID.m4a&guid=10000"

  p "musicu.fcg 未登录取 vkey" -e 'https://y.qq.com/' -H 'Content-Type: application/json' \
    -d "{\"req_0\":{\"module\":\"vkey.GetVkeyServer\",\"method\":\"CgiGetVkey\",\"param\":{\"guid\":\"10000\",\"songmid\":[\"$QQ_MID\"],\"songtype\":[0],\"uin\":\"0\",\"loginflag\":1,\"platform\":\"20\"}},\"loginUin\":\"0\",\"comm\":{\"uin\":\"0\",\"format\":\"json\",\"ct\":24,\"cv\":0}}" \
    'https://u.y.qq.com/cgi-bin/musicu.fcg'
fi

echo
echo "############ 三、网易云取链（换 ID / 换接口）############"
p "enhance/player/url 换免费歌" -e 'https://music.163.com/' \
  'https://music.163.com/api/song/enhance/player/url?ids=%5B1901371647%5D&br=128000'

p "enhance/player/url/v1 level=standard" -e 'https://music.163.com/' \
  'https://music.163.com/api/song/enhance/player/url/v1?ids=%5B1901371647%5D&level=standard&encodeType=mp3'

echo
echo "############ 四、咪咕取链 ############"
p "getPlayInfo dataType=2" -e 'https://music.migu.cn/' \
  'https://music.migu.cn/v3/api/music/audioPlayer/getPlayInfo?dataType=2&data=%7B%22copyrightId%22%3A%2260092994268%22%2C%22contentId%22%3A%2260092994268%22%2C%22resourceType%22%3A2%7D'

p "cms_detail_tag" -e 'https://m.music.migu.cn/' \
  'https://m.music.migu.cn/migu/remoting/cms_detail_tag?resId=60092994268'

echo
echo "=== 全量探测结束 ==="
