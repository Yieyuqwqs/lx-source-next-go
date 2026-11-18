#!/usr/bin/env bash
# 第三轮：验证公开搜索接口，并用真实 ID 跑通「搜索 → 取链 → 歌词」。
set -uo pipefail

echo "=== F) 公开搜索接口 ==="
curl -sS -m 12 -o /tmp/s.json \
  -w 'HTTP %{http_code} | %{size_download} bytes | type=%{content_type}\n' \
  'http://search.kuwo.cn/r.s?all=%E5%91%A8%E6%9D%B0%E4%BC%A6&ft=music&itemset=web_2013&client=kt&pn=0&rn=5&rformat=json&encoding=utf8' 2>&1
head -c 600 /tmp/s.json 2>/dev/null
printf '\n\n'

echo "=== G) 提取第一个 musicrid ==="
grep -oE 'MUSIC_[0-9]+' /tmp/s.json 2>/dev/null | head -3 > /tmp/rid.txt
head -3 /tmp/rid.txt
sed -E 's/[^0-9]//g' /tmp/rid.txt > /tmp/rid2.txt
head -1 /tmp/rid2.txt > /tmp/rid3.txt
RID=$(cat /tmp/rid3.txt 2>/dev/null)
printf '取到 ID = [%s]\n\n' "$RID"

if [ -n "$RID" ]; then
  echo "=== H) 真实 ID 取链 ==="
  curl -sS -m 12 -o /tmp/u.txt \
    -w 'HTTP %{http_code} | %{size_download} bytes\n' \
    "http://antiserver.kuwo.cn/anti.s?type=convert_url&format=mp3&response=url&rid=MUSIC_$RID" 2>&1
  head -c 200 /tmp/u.txt 2>/dev/null
  printf '\n\n'

  echo "=== I) 真实 ID 取歌词（H5） ==="
  curl -sS -m 12 -o /tmp/l.json \
    -w 'HTTP %{http_code} | %{size_download} bytes\n' \
    "https://m.kuwo.cn/newh5/singles/songinfoandlrc?musicId=$RID" 2>&1
  head -c 400 /tmp/l.json 2>/dev/null
  printf '\n\n'

  echo "=== J) 歌词字段结构（前 3 条 lrclist） ==="
  grep -oE '\{"lineLyric":"[^"]{0,80}","time":"[0-9.]+"\}' /tmp/l.json 2>/dev/null | head -3
  printf '\n'
else
  echo "未取到 ID，搜索接口可能已不可用"
fi

echo
echo "=== 第三轮探测结束 ==="
