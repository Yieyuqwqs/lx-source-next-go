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
}

const SOURCE_NAMES = {
  kw: '酷我',
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

const apis = {
  kw: {
    musicUrl: function (musicInfo, quality) {
      const id = musicInfo.songmid || musicInfo.musicId || musicInfo.id || musicInfo.hash
      if (!id) return Promise.reject(new Error('缺少音乐 ID'))
      return httpGet('/url/kw/' + encodeURIComponent(id) + '/' + encodeURIComponent(quality))
        .then(function (body) {
          const data = parseEnvelope(body)
          if (!data.data) throw new Error('没有拿到播放链接')
          return data.data
        })
    },
  },
}

on(EVENT_NAMES.request, function (payload) {
  const source = payload.source
  const action = payload.action
  const info = payload.info || {}

  if (action !== 'musicUrl') {
    return Promise.reject(new Error('不支持的 action：' + action))
  }
  const impl = apis[source]
  if (!impl) {
    return Promise.reject(new Error('不支持的源：' + source))
  }
  return impl.musicUrl(info.musicInfo || {}, info.type)
})

const sources = {}
Object.keys(apis).forEach(function (key) {
  sources[key] = {
    name: SOURCE_NAMES[key] || key,
    type: 'music',
    actions: ['musicUrl'],
    qualitys: QUALITYS[key] || [],
  }
})

send(EVENT_NAMES.inited, { sources: sources, openDevTools: false })
