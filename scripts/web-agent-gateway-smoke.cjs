// Local-only authenticated gateway canary; use the synthetic provider fixture.
const assert = require('node:assert/strict')
const crypto = require('node:crypto')
const base = process.env.WEB_AGENT_API_URL || 'http://127.0.0.1:58080/api/v1'
assert(['127.0.0.1', 'localhost'].includes(new URL(base).hostname), 'Loopback only')
assert.equal(process.env.WEB_AGENT_TEST_EMAIL, 'webagent@localhost.test', 'Disposable test user only')
let token
async function api(path, method = 'GET', body) {
  const response = await fetch(base + path, { method, headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: 'Bearer ' + token } : {}) }, ...(body === undefined ? {} : { body: JSON.stringify(body) }) })
  const json = await response.json()
  assert(response.ok && json.code === 0, `${method} ${path}: ${response.status} ${json.message || ''}`)
  return json.data
}
async function waitFor(read, predicate) {
  const end = Date.now() + 45000
  while (Date.now() < end) {
    const value = await read()
    if (predicate(value)) return value
    await new Promise(resolve => setTimeout(resolve, 200))
  }
  throw new Error('Canary condition timed out')
}
;(async () => {
  token = (await api('/auth/login', 'POST', { email: process.env.WEB_AGENT_TEST_EMAIL, password: process.env.WEB_AGENT_TEST_PASSWORD })).access_token
  const options = await waitFor(() => api('/web-chat/options'), o => o.tasks_enabled)
  assert(options.groups.find(g => g.id === 2)?.name.includes('本地测试'), 'Only the marked local fixture group is permitted')
  const before = await (await fetch('http://127.0.0.1:58081/health')).json()
  assert.equal(typeof before.generations, 'number')
  const session = await api('/web-chat/sessions', 'POST', { group_id: 2, model: 'gpt-4o-mini' })
  const results = []
  for (const kind of ['document', 'slides', 'spreadsheet']) {
    let source
    for (let version = 1; version <= 2; version++) {
      let task = await api(`/web-chat/sessions/${session.id}/tasks`, 'POST', { kind, prompt: '[本地协议测试] 生成或修改测试文件', idempotency_key: crypto.randomUUID(), ...(source ? { source_artifact_id: source.id } : {}) })
      task = await waitFor(() => api(`/web-chat/tasks/${task.id}`), t => ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(t.status))
      assert.equal(task.status, 'succeeded', JSON.stringify({ task: task.id, code: task.error_code, result: task.result }))
      const artifact = await api(`/web-chat/artifacts/${task.result.artifact_id}`)
      assert.equal(artifact.version, version)
      if (source) { assert.equal(artifact.parent_id, source.id); assert.equal(artifact.lineage_id, source.lineage_id) }
      for (const suffix of ['download', 'preview']) {
        const response = await fetch(base + `/web-chat/artifacts/${artifact.id}/${suffix}`, { headers: { Authorization: 'Bearer ' + token } })
        assert.equal(response.status, 200)
        const bytes = Buffer.from(await response.arrayBuffer())
        assert(bytes.subarray(0, suffix === 'preview' ? 5 : 2).equals(Buffer.from(suffix === 'preview' ? '%PDF-' : 'PK')))
        if (suffix === 'download') assert.equal(crypto.createHash('sha256').update(bytes).digest('hex'), artifact.sha256)
      }
      results.push({ task_id: task.id, artifact_id: artifact.id, kind, version, request_id: task.result.generation?.request_id, client_request_id: task.result.generation?.client_request_id, bytes: artifact.size_bytes })
      source = artifact
    }
  }
  const chat = await fetch(base + `/web-chat/sessions/${session.id}/messages`, { method: 'POST', headers: { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' }, body: JSON.stringify({ content: '[本地协议测试] 验证原聊天仍正常终止' }) })
  const stream = await chat.text()
  assert(stream.includes('event: done') && !stream.includes('event: error'), stream.slice(-500))
  const after = await (await fetch('http://127.0.0.1:58081/health')).json()
  assert.equal(after.generations - before.generations, 7, 'No hidden duplicate generation')
  const storage = await api('/web-chat/artifact-storage')
  console.log(JSON.stringify({ session_id: session.id, tasks: results, model_requests: after.generations - before.generations, chat_done: true, storage }, null, 2))
})().catch(error => { console.error(error.message); process.exitCode = 1 })
