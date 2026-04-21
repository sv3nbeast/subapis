# Antigravity Worker Local Enablement

本地启用 Antigravity 外部 worker 的最短路径：

```bash
cd /Users/sven.sun/Desktop/Api/sub2api/backend
make run-local-antigravity-fidelity
```

这个命令会做三件事：

1. 确保存在标准 worker 二进制 `bin/antigravityworker`
2. 尝试构建并优先使用 boringcrypto worker `bin/antigravityworker-boringcrypto`
3. 以本地环境变量方式启动主服务（默认 `127.0.0.1:18731`，不再使用常见的 `8080`）

实际注入的关键环境变量：

```bash
ANTIGRAVITY_EXTERNAL_WORKER_BIN=.../bin/antigravityworker
ANTIGRAVITY_EXTERNAL_WORKER_BIN_BORINGCRYPTO=.../bin/antigravityworker-boringcrypto
ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO=true
SERVER_HOST=127.0.0.1
SERVER_PORT=18731
```

如果本机无法构建 boringcrypto 产物，脚本会自动回退到标准 worker。

## 验证方式

服务启动后，先验证基础健康：

```bash
curl -I http://127.0.0.1:18731/health
```

也可以直接跑本地 smoke：

```bash
cd /Users/sven.sun/Desktop/Api/sub2api/backend
make smoke-antigravity-local
```

再做 Antigravity 实际验证：

1. 用你本地现有可用的 Antigravity 账号
2. 发送一条 Claude/Antigravity 请求
3. 检查日志中是否没有 `external worker binary not configured`
4. 检查是否能正常返回响应

如果想直接让 smoke 发真实请求：

```bash
cd /Users/sven.sun/Desktop/Api/sub2api/backend
ANTIGRAVITY_LOCAL_API_KEY=你的本地Key make smoke-antigravity-local
```

如果你想改成本地其他端口：

```bash
cd /Users/sven.sun/Desktop/Api/sub2api/backend
SERVER_PORT=19731 make run-local-antigravity-fidelity
```

## 只构建，不启动

```bash
cd /Users/sven.sun/Desktop/Api/sub2api/backend
make build-antigravityworker
make build-antigravityworker-boringcrypto
```

## 强制关闭 boringcrypto 优先

```bash
cd /Users/sven.sun/Desktop/Api/sub2api/backend
ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO=false make run-local-antigravity-fidelity
```
