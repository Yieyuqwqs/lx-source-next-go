/**
 * @name __NAME__
 * @description __DESC__
 * @version __VERSION__
 * @author lx-source-next
 */

// 由服务端在返回时把下面的占位符替换成实际值。
// 注意：本文件不使用模板字符串（反引号），因为服务端用 go:embed 原样内嵌，
// 避免与 Go 的原始字符串语法冲突，也让 diff 更干净。

const { EVENT_NAMES, request, on, send } = globalThis.lx

const API_ADDR = '__API_ADDR__'
const API_KEY = '__API_KEY__'

// 每个源支持的音质。
// 酷我 anti 接口会忽略音质参数、固定返回同一份 mp3，
// 因此这里只声明我们能确定的一档，不谎报无损。
const QUALITYS = {
  kw: ['128k'],
  kg: ['128k', '320k', 'flac', 'flac24bit'],
  tx: ['128k', '320k', 'flac', 'flac24bit'],
  mg: ['128k', '320k', 'flac', 'flac24bit'],
  wy: ['128k', '320k', 'flac'],
}

const SOURCE_NAMES = {
  kw: '酷我',
  kg: '酷狗',
  tx: 'QQ音乐',
  mg: '咪咕',
  wy: '网易云',
}

// 统一走服务端接口，不在脚本里直接连上游。
function httpGet(path) {
  return new Promise(function (resolve, reject) {
    const headers = {}
    if (API_KEY) headers['X-Request-Key'] = API_KEY
    request(API_ADDR + path, { method: 'GET', headers: headers, timeout: 15000 }, function (err, resp) {
      if (err) return reject(new Error(err.message || '请求失败'))
      resolve(resp && resp.body)
    })
  })
}

function parseEnvelope(body) {
  let data
  try {
    data = JSON.parse(body)
  } catch (e) {
    throw new Error('服务端返回的不是 JSON')
  }
  if (!data || data.code !== 0) {
    throw new Error((data && data.msg) || '服务端返回错误')
  }
  return data
}

// 取歌曲 id：酷狗等平台每档音质对应不同 hash，优先用与请求音质匹配的那个
function idOf(musicInfo, quality) {
  const files = musicInfo && musicInfo.file
  if (Array.isArray(files)) {
    const hit = files.find(function (f) { return f && f.qual === quality && f.hash })
    if (hit) return hit.hash
  }
  return musicInfo.songmid || musicInfo.hash || musicInfo.musicId || musicInfo.id
}

// 各源共用的取链逻辑，只是平台码不同
function musicUrlOf(pt) {
  return function (musicInfo, quality) {
    const id = idOf(musicInfo, quality)
    if (!id) return Promise.reject(new Error('缺少音乐 ID'))
    return httpGet('/url/' + pt + '/' + encodeURIComponent(id) + '/' + encodeURIComponent(quality))
      .then(function (body) {
        const data = parseEnvelope(body)
        if (!data.data) throw new Error('没有拿到播放链接')
        return data.data
      })
  }
}

// 哪些源提供歌词（五个平台都已接入；酷我走 mlyric，匿名可用）
const LYRIC_SOURCES = { kw: true, kg: true, mg: true, tx: true, wy: true }

// 歌词：服务端统一返回 {lyric, tlyric, rlyric, lxlyric}
function lyricOf(pt) {
  return function (musicInfo) {
    const id = idOf(musicInfo, 'lrc')
    if (!id) return Promise.reject(new Error('缺少音乐 ID'))
    return httpGet('/lrc/' + pt + '/' + encodeURIComponent(id)).then(function (body) {
      const data = parseEnvelope(body)
      return data.data || {}
    })
  }
}

const apis = {
  kw: { musicUrl: musicUrlOf('kw') },
  kg: { musicUrl: musicUrlOf('kg'), lyric: lyricOf('kg') },
  tx: { musicUrl: musicUrlOf('tx'), lyric: lyricOf('tx') },
  mg: { musicUrl: musicUrlOf('mg'), lyric: lyricOf('mg') },
  wy: { musicUrl: musicUrlOf('wy'), lyric: lyricOf('wy') },
}

on(EVENT_NAMES.request, function (payload) {
  const source = payload.source
  const action = payload.action
  const info = payload.info || {}

  const impl = apis[source]
  if (!impl) {
    return Promise.reject(new Error('不支持的源：' + source))
  }
  if (action === 'musicUrl') {
    return impl.musicUrl(info.musicInfo || {}, info.type)
  }
  if (action === 'lyric' && impl.lyric) {
    return impl.lyric(info.musicInfo || {})
  }
  return Promise.reject(new Error('不支持的 action：' + action))
})

const sources = {}
Object.keys(apis).forEach(function (key) {
  sources[key] = {
    name: SOURCE_NAMES[key] || key,
    type: 'music',
    actions: LYRIC_SOURCES[key] ? ['musicUrl', 'lyric'] : ['musicUrl'],
    qualitys: QUALITYS[key] || [],
  }
})

send(EVENT_NAMES.inited, { sources: sources, openDevTools: false })
