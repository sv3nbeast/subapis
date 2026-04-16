#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/remote-cutover-internal-payment.sh

Description:
  Run on the production host. Back up both databases, then migrate
  sub2apipay provider instances, subscription plans, and payment settings
  into sub2api's built-in payment tables/settings.

Environment variables:
  DEPLOY_DIR         sub2api deploy dir. Default: /root/sub2api-deploy
  LEGACY_APP         legacy sub2apipay app container. Default: sub2apipay-app-1
  LEGACY_DB          legacy sub2apipay db container. Default: sub2apipay-db-1
  LEGACY_DB_USER     legacy db user. Default: sub2apipay
  LEGACY_DB_NAME     legacy db name. Default: sub2apipay
  NEW_DB             sub2api postgres container. Default: sub2api-postgres
  NEW_DB_USER        sub2api db user. Default: sub2api
  NEW_DB_NAME        sub2api db name. Default: sub2api
  SKIP_BACKUP        set to 1 to skip pg_dump backups

Notes:
  - This script does NOT migrate legacy orders/audit logs.
  - Existing legacy sub2apipay data is preserved.
  - This script only upserts payment config, plans, and provider instances.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

DEPLOY_DIR="${DEPLOY_DIR:-/root/sub2api-deploy}"
LEGACY_APP="${LEGACY_APP:-sub2apipay-app-1}"
LEGACY_DB="${LEGACY_DB:-sub2apipay-db-1}"
LEGACY_DB_USER="${LEGACY_DB_USER:-sub2apipay}"
LEGACY_DB_NAME="${LEGACY_DB_NAME:-sub2apipay}"
NEW_DB="${NEW_DB:-sub2api-postgres}"
NEW_DB_USER="${NEW_DB_USER:-sub2api}"
NEW_DB_NAME="${NEW_DB_NAME:-sub2api}"
SKIP_BACKUP="${SKIP_BACKUP:-0}"
NEW_PUBLIC_BASE_URL="${NEW_PUBLIC_BASE_URL:-}"

require_cmd docker
require_cmd sed
require_cmd awk
require_cmd grep
require_cmd date
require_cmd mktemp

TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
BACKUP_DIR="${DEPLOY_DIR}/backups/payment-cutover-${TIMESTAMP}"

sql_quote() {
  local value="${1:-}"
  value="${value//\'/\'\'}"
  printf "'%s'" "${value}"
}

bool_sql() {
  local value="${1:-}"
  case "${value}" in
    t|true|TRUE|1|yes|YES|on|ON)
      printf "TRUE"
      ;;
    f|false|FALSE|0|no|NO|off|OFF|'')
      printf "FALSE"
      ;;
    *)
      printf "FALSE"
      ;;
  esac
}

read_container_env() {
  local container="$1"
  local key="$2"
  docker inspect "${container}" --format '{{range .Config.Env}}{{println .}}{{end}}' \
    | awk -F= -v want="${key}" '$1 == want { sub($1"=", ""); print; exit }'
}

read_env_file() {
  local file="$1"
  local key="$2"
  if [[ ! -f "${file}" ]]; then
    return 1
  fi
  grep -E "^${key}=" "${file}" | head -n1 | cut -d= -f2- | sed 's/^"//; s/"$//'
}

run_newdb_sql() {
  local sql="$1"
  docker exec "${NEW_DB}" psql -v ON_ERROR_STOP=1 -U "${NEW_DB_USER}" -d "${NEW_DB_NAME}" -c "${sql}" >/dev/null
}

run_newdb_query() {
  local sql="$1"
  docker exec "${NEW_DB}" psql -v ON_ERROR_STOP=1 -U "${NEW_DB_USER}" -d "${NEW_DB_NAME}" -At -F $'\t' -c "${sql}"
}

run_legacy_query() {
  local sql="$1"
  docker exec "${LEGACY_DB}" psql -v ON_ERROR_STOP=1 -U "${LEGACY_DB_USER}" -d "${LEGACY_DB_NAME}" -At -F $'\t' -c "${sql}"
}

run_legacy_query_pipe() {
  local sql="$1"
  docker exec "${LEGACY_DB}" psql -v ON_ERROR_STOP=1 -U "${LEGACY_DB_USER}" -d "${LEGACY_DB_NAME}" -At -F '|' -c "${sql}"
}

if [[ "${SKIP_BACKUP}" != "1" ]]; then
  mkdir -p "${BACKUP_DIR}"
  docker exec "${NEW_DB}" pg_dump -U "${NEW_DB_USER}" -d "${NEW_DB_NAME}" > "${BACKUP_DIR}/sub2api-pre-cutover.sql"
  docker exec "${LEGACY_DB}" pg_dump -U "${LEGACY_DB_USER}" -d "${LEGACY_DB_NAME}" > "${BACKUP_DIR}/sub2apipay-legacy.sql"
  echo "Backups written to ${BACKUP_DIR}"
fi

OLD_ADMIN_TOKEN="$(read_container_env "${LEGACY_APP}" "ADMIN_TOKEN")"
NEW_KEY_HEX="$(read_env_file "${DEPLOY_DIR}/.env" "TOTP_ENCRYPTION_KEY")"

if [[ -z "${OLD_ADMIN_TOKEN}" ]]; then
  echo "Failed to read ADMIN_TOKEN from ${LEGACY_APP}" >&2
  exit 1
fi

if [[ -z "${NEW_KEY_HEX}" ]]; then
  echo "Failed to read TOTP_ENCRYPTION_KEY from ${DEPLOY_DIR}/.env" >&2
  exit 1
fi

if [[ -z "${NEW_PUBLIC_BASE_URL}" ]]; then
  NEW_PUBLIC_BASE_URL="$(run_newdb_query "select value from settings where key='frontend_url';" | head -n1)"
fi
if [[ -z "${NEW_PUBLIC_BASE_URL}" ]]; then
  NEW_PUBLIC_BASE_URL="$(run_newdb_query "select value from settings where key='api_base_url';" | head -n1)"
fi
NEW_PUBLIC_BASE_URL="${NEW_PUBLIC_BASE_URL%/}"
if [[ -z "${NEW_PUBLIC_BASE_URL}" ]]; then
  echo "Failed to resolve NEW_PUBLIC_BASE_URL from sub2api settings; set NEW_PUBLIC_BASE_URL explicitly." >&2
  exit 1
fi

required_tables=(
  payment_orders
  payment_audit_logs
  subscription_plans
  payment_provider_instances
)

for table in "${required_tables[@]}"; do
  found="$(run_newdb_query "select tablename from pg_tables where schemaname='public' and tablename='${table}';")"
  if [[ -z "${found}" ]]; then
    echo "Required table missing in sub2api DB: ${table}" >&2
    echo "Apply payment migrations before running cutover." >&2
    exit 1
  fi
done

rewrite_and_reencrypt_config() {
  local provider_key="$1"
  local ciphertext="$2"
  local ciphertext_b64
  ciphertext_b64="$(printf '%s' "${ciphertext}" | base64 | tr -d '\n')"
  docker exec -i \
    -e OLD_ADMIN_TOKEN="${OLD_ADMIN_TOKEN}" \
    -e NEW_KEY_HEX="${NEW_KEY_HEX}" \
    -e PROVIDER_KEY="${provider_key}" \
    -e NEW_PUBLIC_BASE_URL="${NEW_PUBLIC_BASE_URL}" \
    -e LEGACY_CONFIG_B64="${ciphertext_b64}" \
    "${LEGACY_APP}" \
    node - <<'NODE'
const { createHash, createDecipheriv, createCipheriv, randomBytes } = require('crypto');

const ciphertext = Buffer.from(process.env.LEGACY_CONFIG_B64 || '', 'base64').toString('utf8');
const oldToken = process.env.OLD_ADMIN_TOKEN || '';
const newKeyHex = process.env.NEW_KEY_HEX || '';
const providerKey = process.env.PROVIDER_KEY || '';
const baseUrl = (process.env.NEW_PUBLIC_BASE_URL || '').replace(/\/$/, '');

function deriveOldKey(secret) {
  return createHash('sha256').update(secret).digest();
}

function decryptLegacy(input, key) {
  const [ivB64, tagB64, dataB64] = input.split(':');
  const iv = Buffer.from(ivB64, 'base64');
  const authTag = Buffer.from(tagB64, 'base64');
  const encrypted = Buffer.from(dataB64, 'base64');
  const decipher = createDecipheriv('aes-256-gcm', key, iv);
  decipher.setAuthTag(authTag);
  return Buffer.concat([decipher.update(encrypted), decipher.final()]).toString('utf8');
}

function encryptNew(plaintext, key) {
  const iv = randomBytes(12);
  const cipher = createCipheriv('aes-256-gcm', key, iv);
  const encrypted = Buffer.concat([cipher.update(plaintext, 'utf8'), cipher.final()]);
  const authTag = cipher.getAuthTag();
  return `${iv.toString('base64')}:${authTag.toString('base64')}:${encrypted.toString('base64')}`;
}

const oldKey = deriveOldKey(oldToken);
const newKey = Buffer.from(newKeyHex, 'hex');
if (newKey.length !== 32) {
  throw new Error(`invalid NEW_KEY_HEX length: ${newKey.length}`);
}
const plaintext = decryptLegacy(ciphertext, oldKey);
const parsed = JSON.parse(plaintext);

if (baseUrl) {
  if (providerKey === 'easypay') {
    parsed.notifyUrl = `${baseUrl}/api/v1/payment/webhook/easypay`;
    parsed.returnUrl = `${baseUrl}/payment/result`;
  } else if (providerKey === 'alipay') {
    parsed.notifyUrl = `${baseUrl}/api/v1/payment/webhook/alipay`;
    parsed.returnUrl = `${baseUrl}/payment/result`;
  } else if (providerKey === 'wxpay') {
    parsed.notifyUrl = `${baseUrl}/api/v1/payment/webhook/wxpay`;
  }
}

process.stdout.write(encryptNew(JSON.stringify(parsed), newKey));
NODE
}

json_array_to_lines() {
  local raw="$1"
  local raw_b64
  raw_b64="$(printf '%s' "${raw}" | base64 | tr -d '\n')"
  docker exec -i -e RAW_JSON_B64="${raw_b64}" "${LEGACY_APP}" node - <<'NODE'
const raw = Buffer.from(process.env.RAW_JSON_B64 || '', 'base64').toString('utf8');
if (!raw) process.exit(0);
try {
  const parsed = JSON.parse(raw);
  if (Array.isArray(parsed)) {
    process.stdout.write(parsed.map((v) => String(v).trim()).filter(Boolean).join('\n'));
  } else {
    process.stdout.write(String(raw));
  }
} catch {
  process.stdout.write(String(raw));
}
NODE
}

echo "Resetting target payment config tables (plans/providers only)..."
run_newdb_sql "TRUNCATE TABLE payment_provider_instances, subscription_plans RESTART IDENTITY CASCADE;"

echo "Migrating provider instances..."
while IFS='|' read -r old_id provider_key name config supported_types enabled sort_order limits refund_enabled; do
  [[ -z "${old_id}" ]] && continue
  new_config="$(rewrite_and_reencrypt_config "${provider_key}" "${config}")"
  payment_mode=""
  clean_supported_types="${supported_types}"
  if [[ "${provider_key}" == "easypay" ]]; then
    if [[ ",${supported_types}," == *",easypay,"* ]]; then
      payment_mode="redirect"
      clean_supported_types="$(printf '%s' "${supported_types}" | sed 's/\(^\|,\)easypay\(,\|$\)/\1/g; s/,,*/,/g; s/^,//; s/,$//')"
    else
      payment_mode="api"
    fi
  fi
  run_newdb_sql "
    INSERT INTO payment_provider_instances (
      provider_key, name, config, supported_types, enabled, payment_mode,
      sort_order, limits, refund_enabled, allow_user_refund
    ) VALUES (
      $(sql_quote "${provider_key}"),
      $(sql_quote "${name}"),
      $(sql_quote "${new_config}"),
      $(sql_quote "${clean_supported_types}"),
      $(bool_sql "${enabled}"),
      $(sql_quote "${payment_mode}"),
      ${sort_order:-0},
      $(sql_quote "${limits}"),
      $(bool_sql "${refund_enabled}"),
      false
    );"
done < <(run_legacy_query_pipe "select id,provider_key,name,config,coalesce(supported_types,''),enabled,sort_order,coalesce(limits,''),refund_enabled from payment_provider_instances order by sort_order,id;")

echo "Migrating subscription plans..."
legacy_channel_group_id_by_name() {
  local plan_name="$1"
  run_legacy_query "select coalesce(group_id::text,'') from channels where name = $(sql_quote "${plan_name}") order by sort_order, id limit 1;"
}

while IFS='|' read -r old_id group_id name price original_price validity_days validity_unit features_json product_name for_sale sort_order; do
  [[ -z "${old_id}" ]] && continue
  resolved_group_id="${group_id}"
  if [[ -z "${resolved_group_id}" ]]; then
    resolved_group_id="$(legacy_channel_group_id_by_name "${name}")"
  fi
  if [[ -z "${resolved_group_id}" ]]; then
    echo "Failed to resolve group_id for legacy plan: ${name}" >&2
    exit 1
  fi
  features_text="$(json_array_to_lines "${features_json}")"
  original_price_sql="NULL"
  if [[ -n "${original_price}" ]]; then
    original_price_sql="${original_price}"
  fi
  run_newdb_sql "
    INSERT INTO subscription_plans (
      group_id, name, description, price, original_price, validity_days,
      validity_unit, features, product_name, for_sale, sort_order
    ) VALUES (
      ${resolved_group_id},
      $(sql_quote "${name}"),
      '',
      ${price},
      ${original_price_sql},
      ${validity_days},
      $(sql_quote "${validity_unit}"),
      $(sql_quote "${features_text}"),
      $(sql_quote "${product_name}"),
      $(bool_sql "${for_sale}"),
      ${sort_order:-0}
    );"
done < <(run_legacy_query_pipe "select id,coalesce(group_id::text,''),name,price::text,coalesce(original_price::text,''),validity_days,validity_unit,coalesce(features,''),coalesce(product_name,''),for_sale,sort_order from subscription_plans order by sort_order,id;")

echo "Migrating payment settings..."
legacy_env="$(docker inspect "${LEGACY_APP}" --format '{{range .Config.Env}}{{println .}}{{end}}')"
legacy_value() {
  local key="$1"
  printf '%s\n' "${legacy_env}" | awk -F= -v want="${key}" '$1 == want { sub($1"=", ""); print; exit }'
}

enabled_payment_types="$(run_legacy_query "select string_agg(distinct trim(x), ',' order by trim(x)) from (select unnest(string_to_array(coalesce(supported_types,''), ',')) as x from payment_provider_instances where enabled = true) t where trim(x) <> '';")"
[[ -z "${enabled_payment_types}" ]] && enabled_payment_types="alipay,wxpay"

declare -A settings_map=(
  [payment_enabled]="true"
  [purchase_subscription_enabled]="false"
  [purchase_subscription_url]=""
  [MIN_RECHARGE_AMOUNT]="$(legacy_value MIN_RECHARGE_AMOUNT)"
  [MAX_RECHARGE_AMOUNT]="$(legacy_value MAX_RECHARGE_AMOUNT)"
  [DAILY_RECHARGE_LIMIT]="$(legacy_value MAX_DAILY_RECHARGE_AMOUNT)"
  [ORDER_TIMEOUT_MINUTES]="$(legacy_value ORDER_TIMEOUT_MINUTES)"
  [MAX_PENDING_ORDERS]="3"
  [ENABLED_PAYMENT_TYPES]="${enabled_payment_types}"
  [BALANCE_PAYMENT_DISABLED]="false"
  [BALANCE_RECHARGE_MULTIPLIER]="1"
  [RECHARGE_FEE_RATE]="0"
  [LOAD_BALANCE_STRATEGY]="round-robin"
  [PRODUCT_NAME_PREFIX]="$(legacy_value PRODUCT_NAME)"
  [PRODUCT_NAME_SUFFIX]=""
  [PAYMENT_HELP_IMAGE_URL]=""
  [PAYMENT_HELP_TEXT]=""
  [CANCEL_RATE_LIMIT_ENABLED]="false"
  [CANCEL_RATE_LIMIT_MAX]="10"
  [CANCEL_RATE_LIMIT_WINDOW]="1"
  [CANCEL_RATE_LIMIT_UNIT]="day"
  [CANCEL_RATE_LIMIT_WINDOW_MODE]="rolling"
)

for key in "${!settings_map[@]}"; do
  value="${settings_map[$key]}"
  run_newdb_sql "
    INSERT INTO settings (key, value, updated_at)
    VALUES ($(sql_quote "${key}"), $(sql_quote "${value}"), NOW())
    ON CONFLICT (key)
    DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();"
done

echo "Payment config migration completed."
echo "Legacy orders/audit logs were intentionally NOT imported."
