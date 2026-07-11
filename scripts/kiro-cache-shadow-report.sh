#!/usr/bin/env bash
set -euo pipefail

redis_bin="${REDIS_CLI_BIN:-redis-cli}"
redis_args=()
if [[ -n "${REDIS_CLI_ARGS:-}" ]]; then
  read -r -a redis_args <<< "$REDIS_CLI_ARGS"
fi
pattern="${1:-kiro_cache_shadow_metrics:v1:*}"

redis() {
  if [[ -n "${REDIS_CLI_ARGS:-}" ]]; then
    "$redis_bin" "${redis_args[@]}" --raw "$@"
  else
    "$redis_bin" --raw "$@"
  fi
}

metric() {
  local key="$1"
  local field="$2"
  local value
  value="$(redis HGET "$key" "$field")"
  printf '%s' "${value:-0}"
}

ratio_pct() {
  awk -v numerator="$1" -v denominator="$2" 'BEGIN { if (denominator == 0) print "NA"; else printf "%.3f", 100 * numerator / denominator }'
}

printf '%s\n' 'key,candidate,requests,warm_requests,bucket_error_pct,max_bucket_share_delta_pp,cost_error_pct,hit_delta_pp'
while IFS= read -r key; do
  [[ -z "$key" ]] && continue
  requests="$(metric "$key" requests)"

  for candidate in current_ratio_0_9 current_ratio_1 protocol_v2; do
    scope="current_warm"
    [[ "$candidate" == "protocol_v2" ]] && scope="protocol_warm"
    candidate_prefix="${scope}_${candidate}"
    actual_prefix="${scope}_actual"
    scoped_context="$(metric "$key" "${scope}_context_tokens")"
    scoped_actual_cost="$(metric "$key" "${actual_prefix}_input_side_cost")"
    scoped_actual_hits="$(metric "$key" "${actual_prefix}_cache_hits")"
    abs_input="$(metric "$key" "${candidate_prefix}_abs_input_error")"
    abs_read="$(metric "$key" "${candidate_prefix}_abs_cache_read_error")"
    abs_creation="$(metric "$key" "${candidate_prefix}_abs_cache_creation_error")"
    abs_bucket="$(awk -v a="$abs_input" -v b="$abs_read" -v c="$abs_creation" 'BEGIN { print a + b + c }')"
    candidate_input="$(metric "$key" "${candidate_prefix}_input_tokens")"
    candidate_read="$(metric "$key" "${candidate_prefix}_cache_read_tokens")"
    candidate_creation="$(metric "$key" "${candidate_prefix}_cache_creation_tokens")"
    actual_input="$(metric "$key" "${actual_prefix}_input_tokens")"
    actual_read="$(metric "$key" "${actual_prefix}_cache_read_tokens")"
    actual_creation="$(metric "$key" "${actual_prefix}_cache_creation_tokens")"
    max_share_delta="$(awk -v ci="$candidate_input" -v cr="$candidate_read" -v cc="$candidate_creation" -v ai="$actual_input" -v ar="$actual_read" -v ac="$actual_creation" -v total="$scoped_context" 'BEGIN { if (total == 0) { print "NA"; exit } di=ci-ai; if(di<0)di=-di; dr=cr-ar; if(dr<0)dr=-dr; dc=cc-ac; if(dc<0)dc=-dc; m=di; if(dr>m)m=dr; if(dc>m)m=dc; printf "%.3f", 100*m/total }')"
    abs_cost="$(metric "$key" "${candidate_prefix}_abs_cost_error")"
    candidate_hits="$(metric "$key" "${candidate_prefix}_cache_hits")"
    warm_field="current_warm_requests"
    [[ "$candidate" == "protocol_v2" ]] && warm_field="protocol_warm_requests"
    warm_requests="$(metric "$key" "$warm_field")"
    hit_delta="$(awk -v a="$candidate_hits" -v b="$scoped_actual_hits" -v n="$warm_requests" 'BEGIN { if (n == 0) print "NA"; else printf "%.3f", 100 * (a - b) / n }')"
    printf '%s,%s,%s,%s,%s,%s,%s,%s\n' \
      "$key" \
      "$candidate" \
      "$requests" \
      "$warm_requests" \
      "$(ratio_pct "$abs_bucket" "$scoped_context")" \
      "$max_share_delta" \
      "$(ratio_pct "$abs_cost" "$scoped_actual_cost")" \
      "$hit_delta"
  done
done < <(redis --scan --pattern "$pattern" | sort)
