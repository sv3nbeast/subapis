#!/bin/bash
# =============================================================================
# Sub2API Bark 监控脚本
# =============================================================================
# 功能：
#   1. 检测 /health 端点是否正常
#   2. 实际调用 AI API 验证上游连通性（claude-opus-4-6）
#   3. 状态变化时通过 Bark 推送通知（异常/恢复），不重复提醒
#
# 用法：
#   chmod +x monitor.sh
#   # 手动运行
#   ./monitor.sh
#   # 加入 crontab（每5分钟）
#   */5 * * * * /path/to/monitor.sh
#
# 配置：
#   复制 monitor.env.example 为 monitor.env 并填写配置
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="${SCRIPT_DIR}/monitor.env"
STATE_FILE="${SCRIPT_DIR}/.monitor_state"

# ---------------------------------------------------------------------------
# 加载配置
# ---------------------------------------------------------------------------
if [ ! -f "$ENV_FILE" ]; then
  echo "[ERROR] 配置文件不存在: $ENV_FILE"
  echo "请复制 monitor.env.example 并填写配置"
  exit 1
fi

source "$ENV_FILE"

# 校验必填项
: "${BARK_URL:?请在 monitor.env 中设置 BARK_URL}"
: "${API_BASE_URL:?请在 monitor.env 中设置 API_BASE_URL}"
: "${API_KEY:?请在 monitor.env 中设置 API_KEY}"

# 默认值
TIMEOUT="${TIMEOUT:-15}"
API_TIMEOUT="${API_TIMEOUT:-60}"
RETRY_COUNT="${RETRY_COUNT:-2}"
RETRY_INTERVAL="${RETRY_INTERVAL:-5}"
BARK_GROUP="${BARK_GROUP:-sub2api}"
BARK_SOUND_ALERT="${BARK_SOUND_ALERT:-alarm}"
BARK_SOUND_RECOVER="${BARK_SOUND_RECOVER:-chord}"
TEST_MODEL="${TEST_MODEL:-claude-opus-4-6}"

# ---------------------------------------------------------------------------
# 状态管理 - 仅记录已通知标记，防止重复推送
#
# 状态文件格式（每行一个 key=value）:
#   health_notified=0       # 0=未通知, 1=已通知异常
#   api_notified=0
# ---------------------------------------------------------------------------
init_state() {
  if [ ! -f "$STATE_FILE" ]; then
    cat > "$STATE_FILE" <<EOF
health_notified=0
api_notified=0
EOF
  fi
}

read_state() {
  source "$STATE_FILE"
}

write_state() {
  cat > "$STATE_FILE" <<EOF
health_notified=${health_notified}
api_notified=${api_notified}
EOF
}

# ---------------------------------------------------------------------------
# Bark 推送
# ---------------------------------------------------------------------------
bark_send() {
  local title="$1"
  local body="$2"
  local sound="${3:-$BARK_SOUND_ALERT}"

  # 使用 POST JSON 方式推送，避免中文 URL 编码问题
  curl -s -o /dev/null --max-time 10 \
    -X POST \
    -H "Content-Type: application/json" \
    -d "{
      \"title\": \"${title}\",
      \"body\": \"${body}\",
      \"group\": \"${BARK_GROUP}\",
      \"sound\": \"${sound}\"
    }" \
    "${BARK_URL}" \
    2>/dev/null || true
}

# ---------------------------------------------------------------------------
# 带重试的请求函数
# 失败后立即重试 RETRY_COUNT 次，间隔 RETRY_INTERVAL 秒
# 返回: 0=最终成功, 1=全部失败
# 副作用: 设置 last_http_code 和 last_body
# ---------------------------------------------------------------------------
last_http_code=""
last_body=""

request_with_retry() {
  local check_name="$1"
  shift
  # 剩余参数是 curl 命令

  local attempt=0
  local max_attempts=$((RETRY_COUNT + 1))  # 首次 + 重试次数

  while [ "$attempt" -lt "$max_attempts" ]; do
    attempt=$((attempt + 1))

    local response
    response=$("$@" 2>/dev/null || echo "__HTTP_CODE__000")

    last_http_code=$(echo "$response" | grep "__HTTP_CODE__" | sed 's/__HTTP_CODE__//')
    last_body=$(echo "$response" | grep -v "__HTTP_CODE__")

    if [ "$last_http_code" = "200" ]; then
      if [ "$attempt" -gt 1 ]; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [RETRY] ${check_name} 第 ${attempt} 次尝试成功"
      fi
      return 0
    fi

    if [ "$attempt" -lt "$max_attempts" ]; then
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] [RETRY] ${check_name} 第 ${attempt} 次失败 (HTTP ${last_http_code}), ${RETRY_INTERVAL}s 后重试..."
      sleep "$RETRY_INTERVAL"
    fi
  done

  echo "[$(date '+%Y-%m-%d %H:%M:%S')] [FAIL] ${check_name} 全部 ${max_attempts} 次尝试均失败"
  return 1
}

# ---------------------------------------------------------------------------
# 检查 1：Health 端点
# ---------------------------------------------------------------------------
check_health() {
  health_ok=0

  if request_with_retry "Health" \
    curl -s -w "\n__HTTP_CODE__%{http_code}" \
    --max-time "$TIMEOUT" \
    "${API_BASE_URL}/health"; then
    # 成功
    health_ok=1
    if [ "$health_notified" = "1" ]; then
      bark_send "Sub2API 已恢复" "Health 端点恢复正常 (HTTP 200)" "$BARK_SOUND_RECOVER"
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] [RECOVER] Health 端点恢复正常"
    fi
    health_notified=0
  else
    # 全部重试失败
    if [ "$health_notified" = "0" ]; then
      bark_send "Sub2API 异常" "Health 端点不可用 (HTTP ${last_http_code}), 已重试 ${RETRY_COUNT} 次" "$BARK_SOUND_ALERT"
      health_notified=1
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ALERT] 已发送 Bark 告警"
    else
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] [SKIP] Health 仍异常，已通知过，不重复推送"
    fi
  fi
}

# ---------------------------------------------------------------------------
# 检查 2：实际 API 调用
# ---------------------------------------------------------------------------
check_api() {
  # 如果 health 都不通，跳过 API 测试
  if [ "$health_ok" = "0" ]; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [SKIP] Health 异常，跳过 API 调用测试"
    return
  fi

  if request_with_retry "API" \
    curl -s -w "\n__HTTP_CODE__%{http_code}" \
    --max-time "$API_TIMEOUT" \
    -H "x-api-key: ${API_KEY}" \
    -H "Content-Type: application/json" \
    -H "anthropic-version: 2023-06-01" \
    -d "{
      \"model\": \"${TEST_MODEL}\",
      \"max_tokens\": 10,
      \"messages\": [{\"role\": \"user\", \"content\": \"hi\"}]
    }" \
    "${API_BASE_URL}/v1/messages"; then
    # 成功
    if [ "$api_notified" = "1" ]; then
      bark_send "API 调用已恢复" "模型 ${TEST_MODEL} 调用恢复正常" "$BARK_SOUND_RECOVER"
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] [RECOVER] API 调用恢复正常"
    fi
    api_notified=0
  else
    # 全部重试失败
    if [ "$api_notified" = "0" ]; then
      local error_msg=""
      if command -v jq &>/dev/null; then
        error_msg=$(echo "$last_body" | jq -r '.error.message // .error // .message // empty' 2>/dev/null || true)
      fi
      if [ -z "$error_msg" ]; then
        error_msg="HTTP ${last_http_code}"
      fi
      error_msg="${error_msg:0:100}"

      bark_send "API 调用异常" "模型 ${TEST_MODEL} 不可用: ${error_msg}, 已重试 ${RETRY_COUNT} 次" "$BARK_SOUND_ALERT"
      api_notified=1
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ALERT] 已发送 Bark 告警"
    else
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] [SKIP] API 仍异常，已通知过，不重复推送"
    fi
  fi
}

# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------
main() {
  init_state
  read_state

  echo "[$(date '+%Y-%m-%d %H:%M:%S')] 开始监控检查..."

  check_health
  check_api

  write_state

  echo "[$(date '+%Y-%m-%d %H:%M:%S')] 检查完成 (health_ok=${health_ok}, health_notified=${health_notified}, api_notified=${api_notified})"
}

main "$@"
