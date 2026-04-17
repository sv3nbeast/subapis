#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/remote-cutover-internal-payment.sh

Description:
  Run on the production host. Back up both databases, then migrate
  sub2apipay provider instances, subscription plans, payment settings,
  legacy orders, and audit logs into sub2api's built-in payment tables/settings.

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
  - Existing legacy sub2apipay data is preserved.
  - This script can be re-run safely; legacy orders/audit logs are deduplicated by legacy order ID and audit fingerprint.
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
SKIP_CONFIG_SYNC="${SKIP_CONFIG_SYNC:-0}"
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

resolve_new_plan_id() {
  local legacy_plan_id="$1"
  [[ -z "${legacy_plan_id}" ]] && return 0

  local cached
  cached="$(run_newdb_query "select payment_plan_id::text from payment_legacy_plan_map where legacy_plan_id = $(sql_quote "${legacy_plan_id}") limit 1;")"
  if [[ -n "${cached}" ]]; then
    printf '%s' "${cached}"
    return 0
  fi

  local legacy_meta
  legacy_meta="$(run_legacy_query_pipe "select coalesce(group_id::text,''),name,price::text,validity_days,validity_unit,sort_order from subscription_plans where id = $(sql_quote "${legacy_plan_id}") limit 1;")"
  [[ -z "${legacy_meta}" ]] && return 0

  local group_id name price validity_days validity_unit sort_order
  IFS='|' read -r group_id name price validity_days validity_unit sort_order <<<"${legacy_meta}"
  if [[ -z "${group_id}" ]]; then
    group_id="$(legacy_channel_group_id_by_name "${name}")"
  fi
  [[ -z "${group_id}" ]] && return 0

  local new_plan_id
  new_plan_id="$(run_newdb_query "
    select id::text
      from subscription_plans
     where group_id = ${group_id}
       and name = $(sql_quote "${name}")
       and price = ${price}
       and validity_days = ${validity_days}
       and validity_unit = $(sql_quote "${validity_unit}")
     order by sort_order, id
     limit 1;")"
  if [[ -n "${new_plan_id}" ]]; then
    run_newdb_sql "
      insert into payment_legacy_plan_map (legacy_plan_id, payment_plan_id, created_at)
      values ($(sql_quote "${legacy_plan_id}"), ${new_plan_id}, now())
      on conflict (legacy_plan_id)
      do update set payment_plan_id = excluded.payment_plan_id;"
    printf '%s' "${new_plan_id}"
  fi
}

resolve_new_provider_id() {
  local legacy_provider_id="$1"
  [[ -z "${legacy_provider_id}" ]] && return 0

  local cached
  cached="$(run_newdb_query "select payment_provider_id::text from payment_legacy_provider_map where legacy_provider_id = $(sql_quote "${legacy_provider_id}") limit 1;")"
  if [[ -n "${cached}" ]]; then
    printf '%s' "${cached}"
    return 0
  fi

  local legacy_meta
  legacy_meta="$(run_legacy_query_pipe "select provider_key,name,sort_order from payment_provider_instances where id = $(sql_quote "${legacy_provider_id}") limit 1;")"
  [[ -z "${legacy_meta}" ]] && return 0

  local provider_key name sort_order
  IFS='|' read -r provider_key name sort_order <<<"${legacy_meta}"
  local new_provider_id
  new_provider_id="$(run_newdb_query "
    select id::text
      from payment_provider_instances
     where provider_key = $(sql_quote "${provider_key}")
       and name = $(sql_quote "${name}")
       and sort_order = ${sort_order:-0}
     order by id
     limit 1;")"
  if [[ -n "${new_provider_id}" ]]; then
    run_newdb_sql "
      insert into payment_legacy_provider_map (legacy_provider_id, payment_provider_id, created_at)
      values ($(sql_quote "${legacy_provider_id}"), ${new_provider_id}, now())
      on conflict (legacy_provider_id)
      do update set payment_provider_id = excluded.payment_provider_id;"
    printf '%s' "${new_provider_id}"
  fi
}

run_newdb_sql "
  CREATE TABLE IF NOT EXISTS payment_legacy_plan_map (
    legacy_plan_id VARCHAR(64) PRIMARY KEY,
    payment_plan_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
  CREATE TABLE IF NOT EXISTS payment_legacy_provider_map (
    legacy_provider_id VARCHAR(64) PRIMARY KEY,
    payment_provider_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
"
legacy_channel_group_id_by_name() {
  local plan_name="$1"
  run_legacy_query "select coalesce(group_id::text,'') from channels where name = $(sql_quote "${plan_name}") order by sort_order, id limit 1;"
}
if [[ "${SKIP_CONFIG_SYNC}" != "1" ]]; then
  echo "Resetting target payment config tables (plans/providers only)..."
  run_newdb_sql "TRUNCATE TABLE payment_provider_instances, subscription_plans RESTART IDENTITY CASCADE;"
  run_newdb_sql "TRUNCATE TABLE payment_legacy_plan_map, payment_legacy_provider_map;"

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
    new_provider_id="$(run_newdb_query "
      select id::text
        from payment_provider_instances
       where provider_key = $(sql_quote "${provider_key}")
         and name = $(sql_quote "${name}")
         and sort_order = ${sort_order:-0}
       order by id desc
       limit 1;")"
    if [[ -n "${new_provider_id}" ]]; then
      run_newdb_sql "
        insert into payment_legacy_provider_map (legacy_provider_id, payment_provider_id, created_at)
        values ($(sql_quote "${old_id}"), ${new_provider_id}, now())
        on conflict (legacy_provider_id)
        do update set payment_provider_id = excluded.payment_provider_id;"
    fi
  done < <(run_legacy_query_pipe "select id,provider_key,name,config,coalesce(supported_types,''),enabled,sort_order,coalesce(limits,''),refund_enabled from payment_provider_instances order by sort_order,id;")

  echo "Migrating subscription plans..."
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
    new_plan_id="$(run_newdb_query "
      select id::text
        from subscription_plans
       where group_id = ${resolved_group_id}
         and name = $(sql_quote "${name}")
         and price = ${price}
         and validity_days = ${validity_days}
         and validity_unit = $(sql_quote "${validity_unit}")
       order by sort_order desc, id desc
       limit 1;")"
    if [[ -n "${new_plan_id}" ]]; then
      run_newdb_sql "
        insert into payment_legacy_plan_map (legacy_plan_id, payment_plan_id, created_at)
        values ($(sql_quote "${old_id}"), ${new_plan_id}, now())
        on conflict (legacy_plan_id)
        do update set payment_plan_id = excluded.payment_plan_id;"
    fi
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
else
  echo "Skipping provider/plan/settings sync; importing legacy history only."
fi

echo "Importing legacy orders..."
legacy_order_sql="
  select
    id,
    user_id::text,
    coalesce(user_email,''),
    coalesce(user_name,''),
    coalesce(user_notes,''),
    amount::text,
    coalesce(pay_amount::text, amount::text),
    coalesce(fee_rate::text,'0'),
    coalesce(recharge_code,''),
    status,
    payment_type,
    coalesce(payment_trade_no,''),
    coalesce(pay_url,''),
    coalesce(qr_code,''),
    coalesce(qr_code_img,''),
    coalesce(order_type,'balance'),
    coalesce(plan_id,''),
    coalesce(subscription_group_id::text,''),
    coalesce(subscription_days::text,''),
    coalesce(provider_instance_id,''),
    coalesce(refund_amount::text,'0'),
    coalesce(refund_reason,''),
    coalesce(refund_at::text,''),
    force_refund,
    coalesce(refund_requested_at::text,''),
    coalesce(refund_request_reason,''),
    coalesce(refund_requested_by::text,''),
    expires_at::text,
    coalesce(paid_at::text,''),
    coalesce(completed_at::text,''),
    coalesce(failed_at::text,''),
    coalesce(failed_reason,''),
    coalesce(client_ip,''),
    coalesce(src_host,''),
    coalesce(src_url,''),
    created_at::text,
    updated_at::text
  from orders
  order by created_at, id;
"

imported_orders=0
skipped_orders=0
while IFS='|' read -r legacy_id user_id user_email user_name user_notes amount pay_amount fee_rate recharge_code status payment_type payment_trade_no pay_url qr_code qr_code_img order_type legacy_plan_id subscription_group_id subscription_days legacy_provider_id refund_amount refund_reason refund_at force_refund refund_requested_at refund_request_reason refund_requested_by expires_at paid_at completed_at failed_at failed_reason client_ip src_host src_url created_at updated_at; do
  [[ -z "${legacy_id}" ]] && continue

  existing_order_id="$(run_newdb_query "select id::text from payment_orders where out_trade_no = $(sql_quote "${legacy_id}") limit 1;")"
  if [[ -n "${existing_order_id}" ]]; then
    skipped_orders=$((skipped_orders + 1))
    continue
  fi

  plan_id_sql="NULL"
  new_plan_id="$(resolve_new_plan_id "${legacy_plan_id}")"
  if [[ -n "${new_plan_id}" ]]; then
    plan_id_sql="${new_plan_id}"
  fi

  provider_id_sql="NULL"
  new_provider_id="$(resolve_new_provider_id "${legacy_provider_id}")"
  if [[ -n "${new_provider_id}" ]]; then
    provider_id_sql="$(sql_quote "${new_provider_id}")"
  fi

  subscription_group_id_sql="NULL"
  [[ -n "${subscription_group_id}" ]] && subscription_group_id_sql="${subscription_group_id}"
  subscription_days_sql="NULL"
  [[ -n "${subscription_days}" ]] && subscription_days_sql="${subscription_days}"
  refund_at_sql="NULL"
  [[ -n "${refund_at}" ]] && refund_at_sql="$(sql_quote "${refund_at}")::timestamptz"
  refund_requested_at_sql="NULL"
  [[ -n "${refund_requested_at}" ]] && refund_requested_at_sql="$(sql_quote "${refund_requested_at}")::timestamptz"
  paid_at_sql="NULL"
  [[ -n "${paid_at}" ]] && paid_at_sql="$(sql_quote "${paid_at}")::timestamptz"
  completed_at_sql="NULL"
  [[ -n "${completed_at}" ]] && completed_at_sql="$(sql_quote "${completed_at}")::timestamptz"
  failed_at_sql="NULL"
  [[ -n "${failed_at}" ]] && failed_at_sql="$(sql_quote "${failed_at}")::timestamptz"

  run_newdb_sql "
    insert into payment_orders (
      user_id, user_email, user_name, user_notes, amount, pay_amount, fee_rate,
      recharge_code, out_trade_no, payment_type, payment_trade_no, pay_url, qr_code, qr_code_img,
      order_type, plan_id, subscription_group_id, subscription_days, provider_instance_id, status,
      refund_amount, refund_reason, refund_at, force_refund, refund_requested_at, refund_request_reason, refund_requested_by,
      expires_at, paid_at, completed_at, failed_at, failed_reason,
      client_ip, src_host, src_url, created_at, updated_at
    ) values (
      ${user_id},
      $(sql_quote "${user_email}"),
      $(sql_quote "${user_name}"),
      $(sql_quote "${user_notes}"),
      ${amount},
      ${pay_amount},
      ${fee_rate},
      $(sql_quote "${recharge_code}"),
      $(sql_quote "${legacy_id}"),
      $(sql_quote "${payment_type}"),
      $(sql_quote "${payment_trade_no}"),
      $(sql_quote "${pay_url}"),
      $(sql_quote "${qr_code}"),
      $(sql_quote "${qr_code_img}"),
      $(sql_quote "${order_type}"),
      ${plan_id_sql},
      ${subscription_group_id_sql},
      ${subscription_days_sql},
      ${provider_id_sql},
      $(sql_quote "${status}"),
      ${refund_amount},
      $(sql_quote "${refund_reason}"),
      ${refund_at_sql},
      $(bool_sql "${force_refund}"),
      ${refund_requested_at_sql},
      $(sql_quote "${refund_request_reason}"),
      $(sql_quote "${refund_requested_by}"),
      $(sql_quote "${expires_at}")::timestamptz,
      ${paid_at_sql},
      ${completed_at_sql},
      ${failed_at_sql},
      $(sql_quote "${failed_reason}"),
      $(sql_quote "${client_ip}"),
      $(sql_quote "${src_host}"),
      $(sql_quote "${src_url}"),
      $(sql_quote "${created_at}")::timestamptz,
      $(sql_quote "${updated_at}")::timestamptz
    );"
  imported_orders=$((imported_orders + 1))
done < <(run_legacy_query_pipe "${legacy_order_sql}")

echo "Importing legacy audit logs..."
legacy_audit_sql="
  select
    order_id,
    action,
    coalesce(detail,''),
    coalesce(operator,'system'),
    created_at::text
  from audit_logs
  order by created_at, id;
"

imported_audits=0
skipped_audits=0
while IFS='|' read -r legacy_order_id action detail operator created_at; do
  [[ -z "${legacy_order_id}" ]] && continue

  new_order_id="$(run_newdb_query "select id::text from payment_orders where out_trade_no = $(sql_quote "${legacy_order_id}") limit 1;")"
  [[ -z "${new_order_id}" ]] && continue

  existing_audit_id="$(run_newdb_query "
    select id::text
      from payment_audit_logs
     where order_id = $(sql_quote "${new_order_id}")
       and action = $(sql_quote "${action}")
       and detail = $(sql_quote "${detail}")
       and operator = $(sql_quote "${operator}")
       and created_at = $(sql_quote "${created_at}")::timestamptz
     limit 1;")"
  if [[ -n "${existing_audit_id}" ]]; then
    skipped_audits=$((skipped_audits + 1))
    continue
  fi

  run_newdb_sql "
    insert into payment_audit_logs (order_id, action, detail, operator, created_at)
    values (
      $(sql_quote "${new_order_id}"),
      $(sql_quote "${action}"),
      $(sql_quote "${detail}"),
      $(sql_quote "${operator}"),
      $(sql_quote "${created_at}")::timestamptz
    );"
  imported_audits=$((imported_audits + 1))
done < <(run_legacy_query_pipe "${legacy_audit_sql}")

echo "Legacy history migration completed."
echo "Imported orders: ${imported_orders}, skipped existing orders: ${skipped_orders}"
echo "Imported audit logs: ${imported_audits}, skipped existing audit logs: ${skipped_audits}"
