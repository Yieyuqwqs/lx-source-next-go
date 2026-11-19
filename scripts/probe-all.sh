#!/usr/bin/env bash
# 决胜轮：QQ 的 purl 到底有没有值；酷狗换多个 hash 找免费资源；咪咕跟重定向。
set -uo pipefail
UA='Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'

echo "############ 一、QQ musicu.fcg：purl 有没有值 ############"
curl -sS -m 12 -A "$UA" -e 'https://y.qq.com/' -o /tmp/qqs.json \
  'https://c.y.qq.com/soso/fcgi-bin/client_search_cp?w=%E5%91%A8%E6%9D%B0%E4%BC%A6&format=json&n=3' 2>&1
grep -oE '"songmid":"[0-9A-Za-z]+"' /tmp/qqs.json | sed -E 's/.*:"([^"]+)"/\1/' > /tmp/mids.txt
printf '候选 songmid:\n'; cat /tmp/mids.txt

N=0
while read -r MID; do
  [ -z "$MID" ] && continue
  N=$((N + 1))
  printf '\n─────────────\n[%d] songmid=%s\n' "$N" "$MID"
  curl -sS -m 12 -A "$UA" -e 'https://y.qq.com/' -H 'Content-Type: application/json' -o /tmp/qqv.json \
    -d "{\"req_0\":{\"module\":\"vkey.GetVkeyServer\",\"method\":\"CgiGetVkey\",\"param\":{\"guid\":\"10000\",\"songmid\":[\"$MID\"],\"songtype\":[0],\"uin\":\"0\",\"loginflag\":1,\"platform\":\"20\"}},\"loginUin\":\"0\",\"comm\":{\"uin\":\"0\",\"format\":\"json\",\"ct\":24,\"cv\":0}}" \
    'https://u.y.qq.com/cgi-bin/musicu.fcg' 2>&1
  printf 'purl 字段: '
  grep -oE '"purl":"[^"]*"' /tmp/qqv.json | head -1
  printf 'result: '
  grep -oE '"result":[0-9]+' /tmp/qqv.json | head -1
  printf '文件名: '
  grep -oE '"filename":"[^"]*"' /tmp/qqv.json | head -1

  SIP=$(grep -oE '"sip":\["[^"]+"' /tmp/qqv.json | head -1 | sed -E 's/.*\["([^"]+)"/\1/')
  PURL=$(grep -oE '"purl":"[^"]+"' /tmp/qqv.json | head -1 | sed -E 's/.*"purl":"([^"]+)"/\1/')
  printf 'sip=%s\n' "$SIP"
  if [ -n "$PURL" ]; then
    printf '拼出直链: %s%s\n' "$SIP" "$PURL"
    curl -sS -m 12 -o /dev/null -w 'HEAD 该直链 → HTTP %{http_code} | %{content_type} | %{size_download}B\n' -I "$SIP$PURL" 2>&1
  else
    printf 'purl 为空，无法拼直链\n'
  fi
done < /tmp/mids.txt

echo
echo "############ 二、酷狗：多个 hash 逐个试 ############"
curl -sS -m 12 -A "$UA" -o /tmp/kg.json \
  'http://mobilecdn.kugou.com/api/v3/search/song?format=json&keyword=%E5%91%A8%E6%9D%B0%E4%BC%A6&page=1&pagesize=6' 2>&1
grep -oE '"hash":"[0-9a-f]{32}"' /tmp/kg.json | sed -E 's/.*:"([^"]+)"/\1/' > /tmp/hashes.txt
while read -r H; do
  [ -z "$H" ] && continue
  KEY=$(printf '%s' "${H}kgcloudv2" | md5sum | cut -d' ' -f1)
  printf '\n[%s] ' "$H"
  curl -sS -m 10 -A "$UA" -H "Referer: http://m.kugou.com/" -o /tmp/kgv.json \
    -w 'HTTP %{http_code} | ' \
    "http://trackercdn.kugou.com/i/v2/?key=$KEY&hash=$H&br=hq&appid=1005&pid=2&cmd=25&behavior=play" 2>&1
  grep -oE '"(url|error|errcode)":[^,}]*' /tmp/kgv.json | head -3
done < /tmp/hashes.txt

echo
echo "############ 三、咪咕：跟随重定向 ############"
p() {
  local title="$1"; shift
  printf '\n─────────────\n%s\n' "$title"
  curl -sSL -m 12 -A "$UA" -o /tmp/m.out -w 'HTTP %{http_code} | %{size_download}B | %{content_type} | 最终:%{url_effective}\n' "$@" 2>&1
  head -c 260 /tmp/m.out 2>/dev/null
  printf '\n'
}
p "cms_detail_tag -L" -e 'https://m.music.migu.cn/' \
  'https://m.music.migu.cn/migu/remoting/cms_detail_tag?resId=60092994268'
p "getPlayInfo -L" -e 'https://music.migu.cn/' \
  'https://music.migu.cn/v3/api/music/audioPlayer/getPlayInfo?dataType=2&data=%7B%22copyrightId%22%3A%2260092994268%22%2C%22contentId%22%3A%2260092994268%22%2C%22resourceType%22%3A2%7D'

echo
echo "=== 决胜轮结束 ==="
