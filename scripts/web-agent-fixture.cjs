// Synthetic loopback provider for local UI/gateway integration. No external I/O.
const http = require('node:http')
const crypto = require('node:crypto')
const marker = '[本地协议测试] 没有调用真实模型。'
let generations = 0
http.createServer((req, res) => {
  if (req.method === 'GET' && req.url === '/health') {
    res.writeHead(200, { 'content-type': 'application/json' })
    return res.end(JSON.stringify({ status: 'ok', generations }))
  }
  let body = ''
  req.on('data', chunk => { body += chunk; if (body.length > 4000000) req.destroy() })
  req.on('end', () => {
    if (req.url !== '/v1/chat/completions') { res.writeHead(404); return res.end() }
    let input
    try { input = JSON.parse(body) } catch { res.writeHead(400); return res.end() }
    generations++
    let content = marker + '\n\n工作台会话和文件使用隔离开发数据库。'
    if (input.stream === false) {
      try {
        const request = JSON.parse(input.messages.at(-1).content)
        const spec = request.source_version || { kind: request.kind, title: '[本地协议测试] 文件' }
        if (request.source_version) spec.title = '[本地协议测试] 修订版文件'
        else if (spec.kind === 'document') spec.sections = [{ heading: '测试说明', paragraphs: [marker] }]
        else if (spec.kind === 'slides') spec.slides = [{ layout: 'cover', title: '本地协议测试', subtitle: marker }]
        else if (spec.kind === 'spreadsheet') spec.sheets = [{ name: '测试数据', columns: [{ name: '说明' }], rows: [[marker]] }]
        else throw new Error('unknown fixture kind')
        content = JSON.stringify(spec)
      } catch { res.writeHead(400); return res.end(JSON.stringify({ error: { message: 'Expected a structured local test task' } })) }
    }
    const id = 'chatcmpl-local-' + crypto.randomUUID()
    const usage = { prompt_tokens: 50, completion_tokens: 30, total_tokens: 80 }
    if (input.stream === false) {
      res.writeHead(200, { 'content-type': 'application/json' })
      return res.end(JSON.stringify({ id, model: input.model, choices: [{ index: 0, message: { role: 'assistant', content }, finish_reason: 'stop' }], usage }))
    }
    res.writeHead(200, { 'content-type': 'text/event-stream' })
    res.write('data: ' + JSON.stringify({ id, model: input.model, choices: [{ index: 0, delta: { content }, finish_reason: null }] }) + '\n\n')
    res.write('data: ' + JSON.stringify({ id, choices: [{ index: 0, delta: {}, finish_reason: 'stop' }], usage }) + '\n\n')
    res.end('data: [DONE]\n\n')
  })
}).listen(Number(process.env.WEB_AGENT_FIXTURE_PORT || 58081), '127.0.0.1', () => console.log('Synthetic Web Agent fixture ready'))
