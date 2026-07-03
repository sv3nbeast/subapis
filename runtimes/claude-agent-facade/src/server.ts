import http from "node:http";
import { randomUUID } from "node:crypto";
import { query, type SDKMessage } from "@anthropic-ai/claude-agent-sdk";

const host = process.env.CLAUDE_AGENT_FACADE_HOST || "127.0.0.1";
const port = Number(process.env.CLAUDE_AGENT_FACADE_PORT || "18181");
const localToken = process.env.CLAUDE_AGENT_FACADE_TOKEN || "";
const defaultCwd = process.env.CLAUDE_AGENT_FACADE_CWD || process.cwd();

type AnthropicMessage = {
  role?: string;
  content?: unknown;
};

type AnthropicMessagesRequest = {
  model?: string;
  messages?: AnthropicMessage[];
  system?: unknown;
  stream?: boolean;
  max_tokens?: number;
  metadata?: Record<string, unknown>;
};

type TextPart = {
  type?: string;
  text?: string;
};

function sanitizeEnv(): NodeJS.ProcessEnv {
  const env = { ...process.env };
  delete env.ANTHROPIC_API_KEY;
  delete env.ANTHROPIC_AUTH_TOKEN;
  delete env.CLAUDE_CODE_USE_BEDROCK;
  delete env.CLAUDE_CODE_USE_VERTEX;
  delete env.CLAUDE_CODE_USE_FOUNDRY;
  return env;
}

function readJson(req: http.IncomingMessage): Promise<unknown> {
  return new Promise((resolve, reject) => {
    let body = "";
    req.setEncoding("utf8");
    req.on("data", (chunk) => {
      body += chunk;
      if (body.length > 2 * 1024 * 1024) {
        reject(new Error("request body too large"));
        req.destroy();
      }
    });
    req.on("end", () => {
      try {
        resolve(body.trim() ? JSON.parse(body) : {});
      } catch {
        reject(new Error("invalid json body"));
      }
    });
    req.on("error", reject);
  });
}

function sendJson(res: http.ServerResponse, status: number, value: unknown): void {
  const data = JSON.stringify(value);
  res.writeHead(status, {
    "content-type": "application/json; charset=utf-8",
    "content-length": Buffer.byteLength(data),
  });
  res.end(data);
}

function isAuthorized(req: http.IncomingMessage): boolean {
  if (!localToken) {
    return true;
  }
  return req.headers.authorization === `Bearer ${localToken}`;
}

function contentToText(content: unknown): string {
  if (typeof content === "string") {
    return content;
  }
  if (Array.isArray(content)) {
    return content
      .map((part) => {
        if (typeof part === "string") {
          return part;
        }
        if (part && typeof part === "object") {
          const typed = part as TextPart;
          if ((typed.type === "text" || !typed.type) && typeof typed.text === "string") {
            return typed.text;
          }
        }
        return "";
      })
      .filter(Boolean)
      .join("\n");
  }
  return "";
}

function buildPrompt(body: AnthropicMessagesRequest): string {
  const lines: string[] = [];
  const system = contentToText(body.system);
  if (system) {
    lines.push("<system>");
    lines.push(system);
    lines.push("</system>");
  }

  const messages = Array.isArray(body.messages) ? body.messages : [];
  for (const message of messages) {
    const role = message.role === "assistant" ? "assistant" : "user";
    const text = contentToText(message.content);
    if (!text) {
      continue;
    }
    lines.push(`<${role}>`);
    lines.push(text);
    lines.push(`</${role}>`);
  }

  if (lines.length === 0) {
    throw new Error("messages must contain at least one text message");
  }
  return lines.join("\n");
}

function extractAssistantText(message: SDKMessage): string {
  if (message.type !== "assistant") {
    return "";
  }
  const content = message.message.content;
  if (!Array.isArray(content)) {
    return "";
  }
  return content
    .map((block) => {
      if (block && typeof block === "object" && "type" in block && block.type === "text" && "text" in block) {
        return String(block.text || "");
      }
      return "";
    })
    .filter(Boolean)
    .join("");
}

function writeSse(res: http.ServerResponse, event: string, data: unknown): void {
  res.write(`event: ${event}\n`);
  res.write(`data: ${JSON.stringify(data)}\n\n`);
}

function createAnthropicMessage(text: string, model: string): Record<string, unknown> {
  return {
    id: `msg_${randomUUID().replaceAll("-", "")}`,
    type: "message",
    role: "assistant",
    model,
    content: [
      {
        type: "text",
        text,
      },
    ],
    stop_reason: "end_turn",
    stop_sequence: null,
    usage: {
      input_tokens: 0,
      output_tokens: 0,
    },
  };
}

async function runAgentText(prompt: string): Promise<{ text: string; rawResult?: unknown }> {
  let text = "";
  let rawResult: unknown;
  for await (const message of query({
    prompt,
    options: {
      cwd: defaultCwd,
      env: sanitizeEnv(),
      maxTurns: Number(process.env.CLAUDE_AGENT_FACADE_MAX_TURNS || "8"),
      permissionMode: "bypassPermissions",
    },
  })) {
    text += extractAssistantText(message);
    if (message.type === "result") {
      rawResult = message;
    }
  }
  return { text, rawResult };
}

async function runAgentStream(prompt: string, model: string, res: http.ServerResponse): Promise<void> {
  const messageId = `msg_${randomUUID().replaceAll("-", "")}`;
  const contentIndex = 0;
  res.writeHead(200, {
    "content-type": "text/event-stream; charset=utf-8",
    "cache-control": "no-cache, no-transform",
    connection: "keep-alive",
    "x-accel-buffering": "no",
  });

  writeSse(res, "message_start", {
    type: "message_start",
    message: {
      id: messageId,
      type: "message",
      role: "assistant",
      model,
      content: [],
      stop_reason: null,
      stop_sequence: null,
      usage: {
        input_tokens: 0,
        output_tokens: 0,
      },
    },
  });
  writeSse(res, "content_block_start", {
    type: "content_block_start",
    index: contentIndex,
    content_block: {
      type: "text",
      text: "",
    },
  });

  for await (const message of query({
    prompt,
    options: {
      cwd: defaultCwd,
      env: sanitizeEnv(),
      maxTurns: Number(process.env.CLAUDE_AGENT_FACADE_MAX_TURNS || "8"),
      permissionMode: "bypassPermissions",
    },
  })) {
    const chunk = extractAssistantText(message);
    if (chunk) {
      writeSse(res, "content_block_delta", {
        type: "content_block_delta",
        index: contentIndex,
        delta: {
          type: "text_delta",
          text: chunk,
        },
      });
    }
  }

  writeSse(res, "content_block_stop", {
    type: "content_block_stop",
    index: contentIndex,
  });
  writeSse(res, "message_delta", {
    type: "message_delta",
    delta: {
      stop_reason: "end_turn",
      stop_sequence: null,
    },
    usage: {
      output_tokens: 0,
    },
  });
  writeSse(res, "message_stop", {
    type: "message_stop",
  });
  res.end();
}

async function handleMessages(req: http.IncomingMessage, res: http.ServerResponse): Promise<void> {
  if (!isAuthorized(req)) {
    sendJson(res, 401, { type: "error", error: { type: "authentication_error", message: "unauthorized" } });
    return;
  }
  const body = (await readJson(req)) as AnthropicMessagesRequest;
  const model = body.model || "claude-agent-sdk";
  const prompt = buildPrompt(body);

  if (body.stream) {
    await runAgentStream(prompt, model, res);
    return;
  }

  const { text, rawResult } = await runAgentText(prompt);
  sendJson(res, 200, {
    ...createAnthropicMessage(text, model),
    _facade: {
      mode: "claude_agent_sdk_text_only_poc",
      raw_result: rawResult,
    },
  });
}

const server = http.createServer((req, res) => {
  void (async () => {
    try {
      const url = new URL(req.url || "/", `http://${req.headers.host || `${host}:${port}`}`);
      if (req.method === "GET" && url.pathname === "/health") {
        sendJson(res, 200, {
          ok: true,
          service: "claude-agent-facade",
          text_only_poc: true,
        });
        return;
      }
      if (req.method === "POST" && (url.pathname === "/v1/messages" || url.pathname === "/anthropic/v1/messages")) {
        await handleMessages(req, res);
        return;
      }
      sendJson(res, 404, {
        type: "error",
        error: {
          type: "not_found_error",
          message: `no route for ${req.method} ${url.pathname}`,
        },
      });
    } catch (error) {
      sendJson(res, 500, {
        type: "error",
        error: {
          type: "api_error",
          message: error instanceof Error ? error.message : String(error),
        },
      });
    }
  })();
});

server.listen(port, host, () => {
  console.log(`claude-agent-facade listening on http://${host}:${port}`);
  console.log(`cwd=${defaultCwd}`);
});
