# Claude Agent Facade PoC

本目录是独立本地 PoC，不接入 `backend/` 主服务热路径。

它暴露一个最小 Anthropic Messages 兼容入口：

- `GET /health`
- `POST /v1/messages`
- `POST /anthropic/v1/messages`

内部调用 `@anthropic-ai/claude-agent-sdk`，用于验证：

```text
Claude CLI / Anthropic Messages client -> local facade -> Agent SDK -> Claude subscription
```

## 限制

这是 text-only PoC：

- 不实现 client-side tool bridge。
- 不保证 Claude CLI 的完整工具体验。
- 不保留 Anthropic API prompt cache 语义。
- SSE 只包装文本事件。

## 启动

```bash
cd runtimes/claude-agent-facade
npm install

# 推荐先用 claude setup-token 并导出 CLAUDE_CODE_OAUTH_TOKEN。
# 不要设置 ANTHROPIC_API_KEY，否则可能走 API key。
unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN

npm run dev
```

可选环境变量：

```bash
export CLAUDE_AGENT_FACADE_HOST=127.0.0.1
export CLAUDE_AGENT_FACADE_PORT=18181
export CLAUDE_AGENT_FACADE_TOKEN=local-secret
export CLAUDE_AGENT_FACADE_CWD=/path/to/test/workspace
export CLAUDE_AGENT_FACADE_MAX_TURNS=8
```

## curl 测试

```bash
curl -sS http://127.0.0.1:18181/v1/messages \
  -H 'content-type: application/json' \
  -d '{"model":"claude-sonnet-4-5","max_tokens":256,"messages":[{"role":"user","content":"用一句话介绍你自己"}]}'
```

流式：

```bash
curl -N http://127.0.0.1:18181/v1/messages \
  -H 'content-type: application/json' \
  -d '{"model":"claude-sonnet-4-5","max_tokens":256,"stream":true,"messages":[{"role":"user","content":"数到3，每个数字一行"}]}'
```

## Claude CLI 试验

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:18181
export ANTHROPIC_AUTH_TOKEN=local-secret
claude
```

如果没有设置 `CLAUDE_AGENT_FACADE_TOKEN`，`ANTHROPIC_AUTH_TOKEN` 可以是任意非空值。
