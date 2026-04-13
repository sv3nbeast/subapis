#!/bin/bash
# =============================================================================
# Sub2API Bark 监控脚本
# =============================================================================
# 功能：
#   1. 检测 /health 端点是否正常
#   2. 读取服务状态探针结果验证模型可用性（不再重复发起模型请求）
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

# 默认值
TIMEOUT="${TIMEOUT:-15}"
# 服务状态接口读取超时（默认 15s）。
API_TIMEOUT="${API_TIMEOUT:-15}"
RETRY_COUNT="${RETRY_COUNT:-2}"
RETRY_INTERVAL="${RETRY_INTERVAL:-5}"
BARK_GROUP="${BARK_GROUP:-sub2api}"
BARK_SOUND_ALERT="${BARK_SOUND_ALERT:-alarm}"
BARK_SOUND_RECOVER="${BARK_SOUND_RECOVER:-chord}"
BARK_NOTIFY_ALERT="${BARK_NOTIFY_ALERT:-true}"
BARK_NOTIFY_RECOVER="${BARK_NOTIFY_RECOVER:-true}"
TEST_MODEL="${TEST_MODEL:-claude-opus-4-6}"
STATUS_API_PATH="${STATUS_API_PATH:-/api/v1/status/models}"
SCHEDULING_THRESHOLD="${SCHEDULING_THRESHOLD:-10}"

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
health_down_since_epoch=0
api_notified=0
api_down_since_epoch=0
scheduling_notified=0
scheduling_low_since_epoch=0
EOF
  fi
}

read_state() {
  source "$STATE_FILE"
  : "${health_notified:=0}"
  : "${health_down_since_epoch:=0}"
  : "${api_notified:=0}"
  : "${api_down_since_epoch:=0}"
  : "${scheduling_notified:=0}"
  : "${scheduling_low_since_epoch:=0}"
}

write_state() {
  cat > "$STATE_FILE" <<EOF
health_notified=${health_notified}
health_down_since_epoch=${health_down_since_epoch}
api_notified=${api_notified}
api_down_since_epoch=${api_down_since_epoch}
scheduling_notified=${scheduling_notified}
scheduling_low_since_epoch=${scheduling_low_since_epoch}
EOF
}

now_epoch() {
  date +%s
}

format_epoch() {
  local epoch="${1:-0}"
  if [ -z "$epoch" ] || [ "$epoch" = "0" ]; then
    return 1
  fi

  if date -d "@${epoch}" "+%Y-%m-%d %H:%M:%S" >/dev/null 2>&1; then
    date -d "@${epoch}" "+%Y-%m-%d %H:%M:%S"
    return 0
  fi

  if date -r "${epoch}" "+%Y-%m-%d %H:%M:%S" >/dev/null 2>&1; then
    date -r "${epoch}" "+%Y-%m-%d %H:%M:%S"
    return 0
  fi

  return 1
}

format_duration() {
  local seconds="${1:-0}"
  if [ -z "$seconds" ] || [ "$seconds" -le 0 ] 2>/dev/null; then
    echo "0s"
    return
  fi

  local hours=$((seconds / 3600))
  local minutes=$(((seconds % 3600) / 60))
  local secs=$((seconds % 60))
  local parts=()

  if [ "$hours" -gt 0 ]; then
    parts+=("${hours}h")
  fi
  if [ "$minutes" -gt 0 ]; then
    parts+=("${minutes}m")
  fi
  if [ "$secs" -gt 0 ] || [ "${#parts[@]}" -eq 0 ]; then
    parts+=("${secs}s")
  fi

  printf "%s" "${parts[0]}"
  local i
  for ((i = 1; i < ${#parts[@]}; i++)); do
    printf " %s" "${parts[$i]}"
  done
}

build_recovery_suffix() {
  local start_epoch="${1:-0}"
  local now_ts
  now_ts=$(now_epoch)
  local duration_text start_text

  duration_text="未知"
  if [ -n "$start_epoch" ] && [ "$start_epoch" != "0" ] 2>/dev/null && [ "$now_ts" -ge "$start_epoch" ] 2>/dev/null; then
    duration_text=$(format_duration $((now_ts - start_epoch)))
  fi

  if start_text=$(format_epoch "$start_epoch"); then
    printf "；异常开始于 %s，持续 %s" "$start_text" "$duration_text"
  else
    printf "；异常开始时间未知，持续 %s" "$duration_text"
  fi
}

# ---------------------------------------------------------------------------
# Bark 推送
# ---------------------------------------------------------------------------
bark_send() {
  local title="$1"
  local body="$2"
  local sound="${3:-$BARK_SOUND_ALERT}"
  local response http_code

  # 使用 POST JSON 方式推送，避免中文 URL 编码问题
  response=$(curl -sS --max-time 10 \
    -X POST \
    -H "Content-Type: application/json" \
    -d "{
      \"title\": \"${title}\",
      \"body\": \"${body}\",
      \"group\": \"${BARK_GROUP}\",
      \"sound\": \"${sound}\"
    }" \
    -w "\n__HTTP_CODE__%{http_code}" \
    "${BARK_URL}" \
    2>/dev/null || true)

  http_code=$(echo "$response" | grep "__HTTP_CODE__" | sed 's/__HTTP_CODE__//')
  if [ -n "$http_code" ] && [ "$http_code" -ge 200 ] && [ "$http_code" -lt 300 ]; then
    return 0
  fi
  return 1
}

is_truthy() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

send_alert_if_enabled() {
  local title="$1"
  local body="$2"
  if is_truthy "$BARK_NOTIFY_ALERT"; then
    bark_send "$title" "$body" "$BARK_SOUND_ALERT"
    return 0
  fi
  return 1
}

send_recover_if_enabled() {
  local title="$1"
  local body="$2"
  if is_truthy "$BARK_NOTIFY_RECOVER"; then
    bark_send "$title" "$body" "$BARK_SOUND_RECOVER"
    return 0
  fi
  return 1
}

extract_json_string_field() {
  local field="$1"
  if command -v jq >/dev/null 2>&1; then
    echo "$last_body" | jq -r --arg field "$field" '.[$field] // empty' 2>/dev/null || true
    return
  fi

  echo "$last_body" | tr -d '\n' | sed -n "s/.*\"${field}\":\"\\([^\"]*\\)\".*/\\1/p" | head -n 1
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
      local recovery_suffix=""
      recovery_suffix=$(build_recovery_suffix "$health_down_since_epoch")
      if send_recover_if_enabled "Sub2API 已恢复" "Health 端点恢复正常 (HTTP 200)${recovery_suffix}"; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [RECOVER] Health 端点恢复正常"
      else
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [RECOVER-SILENT] Health 端点恢复正常，但恢复通知已禁用"
      fi
    fi
    health_notified=0
    health_down_since_epoch=0
  else
    # 全部重试失败
    if [ "$health_notified" = "0" ]; then
      health_notified=1
      health_down_since_epoch=$(now_epoch)
      if send_alert_if_enabled "Sub2API 异常" "Health 端点不可用 (HTTP ${last_http_code}), 已重试 ${RETRY_COUNT} 次，开始于 $(format_epoch "$health_down_since_epoch")"; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ALERT] 已发送 Bark 告警"
      else
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ALERT-SILENT] Health 异常已记录，异常通知已禁用"
      fi
    else
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] [SKIP] Health 仍异常，已通知过，不重复推送"
    fi
  fi
}

# ---------------------------------------------------------------------------
# 检查 2：服务状态探针结果
# ---------------------------------------------------------------------------
check_api() {
  # 如果 health 都不通，跳过 API 测试
  if [ "$health_ok" = "0" ]; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [SKIP] Health 异常，跳过服务状态检查"
    return
  fi

  local status_url="${API_BASE_URL%/}${STATUS_API_PATH%/}/${TEST_MODEL}"

  if request_with_retry "StatusProbe" \
    curl -s -w "\n__HTTP_CODE__%{http_code}" \
    --max-time "$API_TIMEOUT" \
    "${status_url}"; then
    local current_status error_msg
    current_status=$(extract_json_string_field "current_status")
    error_msg=$(extract_json_string_field "error_message")

    if [ "$current_status" = "operational" ]; then
      if [ "$api_notified" = "1" ]; then
        local recovery_suffix=""
        recovery_suffix=$(build_recovery_suffix "$api_down_since_epoch")
        if send_recover_if_enabled "API 调用已恢复" "模型 ${TEST_MODEL} 调用恢复正常${recovery_suffix}"; then
          echo "[$(date '+%Y-%m-%d %H:%M:%S')] [RECOVER] 模型 ${TEST_MODEL} 恢复正常"
        else
          echo "[$(date '+%Y-%m-%d %H:%M:%S')] [RECOVER-SILENT] 模型 ${TEST_MODEL} 恢复正常，但恢复通知已禁用"
        fi
      fi
      api_notified=0
      api_down_since_epoch=0
      return
    fi

    if [ -z "$current_status" ]; then
      current_status="unknown"
    fi
    if [ -z "$error_msg" ]; then
      error_msg="当前状态为 ${current_status}"
    fi
    error_msg="${error_msg:0:160}"

    if [ "$api_notified" = "0" ]; then
      api_notified=1
      api_down_since_epoch=$(now_epoch)
      if send_alert_if_enabled "API 调用异常" "模型 ${TEST_MODEL} 不可用: ${error_msg}，开始于 $(format_epoch "$api_down_since_epoch")"; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ALERT] 模型 ${TEST_MODEL} 当前状态 ${current_status}, 已发送 Bark 告警"
      else
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ALERT-SILENT] 模型 ${TEST_MODEL} 当前状态 ${current_status}，异常通知已禁用"
      fi
    else
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] [SKIP] 模型 ${TEST_MODEL} 仍为 ${current_status}，已通知过"
    fi
  else
    # 全部重试失败
    if [ "$api_notified" = "0" ]; then
      local error_msg=""
      if [ "$last_http_code" = "404" ]; then
        error_msg="模型 ${TEST_MODEL} 未配置到服务状态探针"
      elif command -v jq &>/dev/null; then
        error_msg=$(echo "$last_body" | jq -r '.error.message // .error // .message // empty' 2>/dev/null || true)
      fi
      if [ -z "$error_msg" ]; then
        error_msg="HTTP ${last_http_code}"
      fi
      error_msg="${error_msg:0:160}"

      api_notified=1
      api_down_since_epoch=$(now_epoch)
      if send_alert_if_enabled "API 调用异常" "模型 ${TEST_MODEL} 状态读取失败: ${error_msg}, 已重试 ${RETRY_COUNT} 次，开始于 $(format_epoch "$api_down_since_epoch")"; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ALERT] 已发送 Bark 告警"
      else
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ALERT-SILENT] 服务状态读取异常已记录，异常通知已禁用"
      fi
    else
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] [SKIP] 服务状态读取仍异常，已通知过，不重复推送"
    fi
  fi
}

# ---------------------------------------------------------------------------
# 检查 3：调度可用率（可用账号 < SCHEDULING_THRESHOLD% 时预警）
# 直接查询本地数据库，无需暴露 API 端点
# ---------------------------------------------------------------------------
check_scheduling() {
  local pg_container="${PG_CONTAINER:-sub2api-postgres}"
  local pg_user="${PG_USER:-sub2api}"
  local pg_db="${PG_DB:-sub2api}"

  # 通过 docker exec 查询账号调度状态
  local query_result
  query_result=$(docker exec "$pg_container" psql -U "$pg_user" -d "$pg_db" -t -A -F',' -c "
    SELECT
      COUNT(*) FILTER (WHERE schedulable = true AND status = 'active') AS total,
      COUNT(*) FILTER (WHERE schedulable = true AND status = 'active'
        AND (rate_limit_reset_at IS NULL OR rate_limit_reset_at <= NOW())
        AND (temp_unschedulable_until IS NULL OR temp_unschedulable_until <= NOW())) AS available,
      COUNT(*) FILTER (WHERE schedulable = true AND status = 'active'
        AND rate_limit_reset_at IS NOT NULL AND rate_limit_reset_at > NOW()) AS rate_limited,
      COUNT(*) FILTER (WHERE schedulable = true AND status = 'active'
        AND temp_unschedulable_until IS NOT NULL AND temp_unschedulable_until > NOW()) AS temp_unsched
    FROM accounts WHERE deleted_at IS NULL;
  " 2>/dev/null || echo "")

  if [ -z "$query_result" ]; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [WARN] 无法查询数据库调度状态"
    return
  fi

  local total available rate_limited temp_unsched
  IFS=',' read -r total available rate_limited temp_unsched <<< "$query_result"

  # 去掉空格
  total=$(echo "$total" | tr -d ' ')
  available=$(echo "$available" | tr -d ' ')
  rate_limited=$(echo "$rate_limited" | tr -d ' ')
  temp_unsched=$(echo "$temp_unsched" | tr -d ' ')

  if [ "$total" = "0" ] || [ -z "$total" ]; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [INFO] 无可调度账号"
    return
  fi

  local available_pct
  available_pct=$(awk "BEGIN {printf \"%.1f\", ($available / $total) * 100}")

  echo "[$(date '+%Y-%m-%d %H:%M:%S')] [INFO] 调度状态: 可用 ${available}/${total} (${available_pct}%), 限流 ${rate_limited}, 临时不可调度 ${temp_unsched}"

  local is_low
  is_low=$(awk "BEGIN {print ($available_pct < $SCHEDULING_THRESHOLD) ? 1 : 0}")

  if [ "$is_low" = "1" ]; then
    if [ "$scheduling_notified" = "0" ]; then
      scheduling_notified=1
      scheduling_low_since_epoch=$(now_epoch)
      if send_alert_if_enabled "账号可用率过低" "可用 ${available}/${total} (${available_pct}%), 限流 ${rate_limited}, 临时不可调度 ${temp_unsched}，开始于 $(format_epoch "$scheduling_low_since_epoch")"; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ALERT] 调度可用率 ${available_pct}% < ${SCHEDULING_THRESHOLD}%, 已发送 Bark 告警"
      else
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ALERT-SILENT] 调度可用率过低已记录，异常通知已禁用"
      fi
    else
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] [SKIP] 调度可用率仍低，已通知过"
    fi
  else
    if [ "$scheduling_notified" = "1" ]; then
      local recovery_suffix=""
      recovery_suffix=$(build_recovery_suffix "$scheduling_low_since_epoch")
      if send_recover_if_enabled "账号可用率恢复" "可用 ${available}/${total} (${available_pct}%)${recovery_suffix}"; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [RECOVER] 调度可用率恢复正常"
      else
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] [RECOVER-SILENT] 调度可用率恢复正常，但恢复通知已禁用"
      fi
    fi
    scheduling_notified=0
    scheduling_low_since_epoch=0
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
  check_scheduling

  write_state

  echo "[$(date '+%Y-%m-%d %H:%M:%S')] 检查完成 (health_ok=${health_ok}, health_notified=${health_notified}, api_notified=${api_notified}, scheduling_notified=${scheduling_notified})"
}

main "$@"
