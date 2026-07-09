#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
START_STACK="${START_STACK:-1}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

PASSED=0
FAILED=0

log() {
  printf "\n%s\n" "$*"
}

pass() {
  PASSED=$((PASSED + 1))
  printf "PASS %s\n" "$*"
}

fail() {
  FAILED=$((FAILED + 1))
  printf "FAIL %s\n" "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

require_cmd curl
require_cmd jq
require_cmd docker

fixture() {
  local invoice_id="$1"
  local out="$TMP_DIR/${invoice_id}.json"

  jq --arg id "$invoice_id" '.fixtures[] | select(.id == $id)' data/sample-invoices.json > "$out"

  if [ ! -s "$out" ] || [ "$(cat "$out")" = "null" ]; then
    fail "fixture not found: $invoice_id"
  fi

  printf "%s" "$out"
}

wait_for_gateway() {
  log "Waiting for gateway health..."

  for _ in $(seq 1 90); do
    if curl -fsS "$BASE_URL/healthz" >/dev/null 2>&1; then
      pass "gateway healthz reachable"
      sleep 8
      return
    fi
    sleep 2
  done

  fail "gateway did not become healthy"
}

submit_invoice() {
  local invoice_id="$1"
  local correlation_id="$2"
  local expected_code="$3"
  local payload
  local body
  local code

  payload="$(fixture "$invoice_id")"
  body="$TMP_DIR/submit-${invoice_id}-${correlation_id}.json"

  for attempt in $(seq 1 20); do
    code="$(
      curl -sS \
        -o "$body" \
        -w "%{http_code}" \
        -H "Content-Type: application/json" \
        -H "X-Correlation-Id: $correlation_id" \
        --data @"$payload" \
        "$BASE_URL/submissions" || true
    )"

    if [ "$code" = "$expected_code" ]; then
      cat "$body"
      return
    fi

    if [ "$code" = "502" ] || [ "$code" = "000" ]; then
      sleep 2
      continue
    fi

    printf "Unexpected response body:\n" >&2
    cat "$body" >&2 || true
    printf "\n" >&2
    fail "$invoice_id expected HTTP $expected_code but got HTTP $code"
  done

  printf "Last response body:\n" >&2
  cat "$body" >&2 || true
  printf "\n" >&2
  fail "$invoice_id expected HTTP $expected_code but service did not become ready"
}

get_submission_status() {
  local tracking_id="$1"
  curl -sS \
    -H "X-Correlation-Id: verify-get-$tracking_id" \
    "$BASE_URL/submissions/$tracking_id" | jq -r '.status // empty'
}

wait_for_submission_status() {
  local tracking_id="$1"
  local expected="$2"
  local label="$3"

  for _ in $(seq 1 60); do
    local status
    status="$(get_submission_status "$tracking_id" || true)"

    if [ "$status" = "$expected" ]; then
      pass "$label -> $expected"
      return
    fi

    sleep 1
  done

  local body
  body="$(curl -sS -H "X-Correlation-Id: verify-final-$tracking_id" "$BASE_URL/submissions/$tracking_id" || true)"
  printf "Final response body:\n%s\n" "$body" >&2
  fail "$label did not reach $expected"
}

wait_for_approval_pending() {
  local tracking_id="$1"
  local label="$2"

  for _ in $(seq 1 60); do
    if curl -sS \
      -H "X-Correlation-Id: verify-approvals-$tracking_id" \
      "$BASE_URL/approvals" |
      jq -e --arg id "$tracking_id" '.items[]? | select(.tracking_id == $id and .status == "PENDING")' >/dev/null; then
      pass "$label approval item pending"
      return
    fi

    sleep 1
  done

  curl -sS -H "X-Correlation-Id: verify-approvals-final" "$BASE_URL/approvals" >&2 || true
  printf "\n" >&2
  fail "$label approval item did not become pending"
}

approve_submission() {
  local tracking_id="$1"
  local correlation_id="$2"
  local body="$TMP_DIR/approve-$tracking_id.json"
  local code

  code="$(
    curl -sS \
      -o "$body" \
      -w "%{http_code}" \
      -H "Content-Type: application/json" \
      -H "X-Correlation-Id: $correlation_id" \
      --data '{"actor":"verification@approvalflow.local","reason":"Approved by verification command."}' \
      "$BASE_URL/approvals/$tracking_id/approve"
  )"

  if [ "$code" != "200" ]; then
    printf "Approve response body:\n" >&2
    cat "$body" >&2 || true
    printf "\n" >&2
    fail "approve $tracking_id expected HTTP 200 but got HTTP $code"
  fi

  pass "$tracking_id human approval accepted"
}

wait_for_audit_actions() {
  local correlation_id="$1"
  shift

  local body="$TMP_DIR/audit-$correlation_id.json"

  for _ in $(seq 1 60); do
    curl -sS \
      -H "X-Correlation-Id: verify-audit-$correlation_id" \
      "$BASE_URL/audit/$correlation_id" > "$body" || true

    local missing=0
    for action in "$@"; do
      if ! jq -e --arg action "$action" '.events[]? | select(.action == $action)' "$body" >/dev/null; then
        missing=1
        break
      fi
    done

    if [ "$missing" -eq 0 ]; then
      pass "$correlation_id audit contains: $*"
      return
    fi

    sleep 1
  done

  printf "Audit body for %s:\n" "$correlation_id" >&2
  cat "$body" >&2 || true
  printf "\n" >&2
  fail "$correlation_id audit missing one or more actions: $*"
}

assert_no_audit_action() {
  local correlation_id="$1"
  local action="$2"
  local body="$TMP_DIR/audit-no-$correlation_id.json"

  curl -sS \
    -H "X-Correlation-Id: verify-audit-no-$correlation_id" \
    "$BASE_URL/audit/$correlation_id" > "$body" || true

  if jq -e --arg action "$action" '.events[]? | select(.action == $action)' "$body" >/dev/null; then
    printf "Audit body for %s:\n" "$correlation_id" >&2
    cat "$body" >&2 || true
    printf "\n" >&2
    fail "$correlation_id unexpectedly contains audit action: $action"
  fi

  pass "$correlation_id does not contain $action"
}

assert_approval_violation() {
  local tracking_id="$1"
  local violation="$2"
  local label="$3"

  for _ in $(seq 1 60); do
    if curl -sS \
      -H "X-Correlation-Id: verify-approval-violation-$tracking_id" \
      "$BASE_URL/approvals" |
      jq -e --arg id "$tracking_id" --arg violation "$violation" '
        .items[]?
        | select(
            .tracking_id == $id
            and (.violations[]? == $violation)
          )
      ' >/dev/null; then
      pass "$label approval item contains $violation"
      return
    fi

    sleep 1
  done

  curl -sS -H "X-Correlation-Id: verify-approval-violation-final" "$BASE_URL/approvals" >&2 || true
  printf "\n" >&2
  fail "$label approval item did not contain $violation"
}

assert_anti_cheese_approval() {
  local tracking_id="$1"

  for _ in $(seq 1 60); do
    if curl -sS \
      -H "X-Correlation-Id: verify-prompt-injection-approvals" \
      "$BASE_URL/approvals" |
      jq -e --arg id "$tracking_id" '
        .items[]?
        | select(
            .tracking_id == $id
            and .status == "PENDING"
            and .agent_recommended == "approve"
          )
      ' >/dev/null; then
      pass "prompt-injection agent recommended approve but router kept human review"
      return
    fi

    sleep 1
  done

  curl -sS -H "X-Correlation-Id: verify-prompt-injection-final" "$BASE_URL/approvals" >&2 || true
  printf "\n" >&2
  fail "prompt-injection approval item not found with agent_recommended=approve"
}

if [ "$START_STACK" = "1" ]; then
  log "Starting fresh docker compose stack..."
  docker compose down --volumes --remove-orphans >/dev/null
  docker compose up --build -d
fi

wait_for_gateway

RUN_ID="verify-$(date +%s)"

log "Scenario: INV-1001 auto approve -> PAID"
CORR_1001="$RUN_ID-1001"
RESP_1001="$(submit_invoice "INV-1001" "$CORR_1001" "202")"
TRACK_1001="$(printf "%s" "$RESP_1001" | jq -r '.tracking_id')"

[ "$TRACK_1001" != "null" ] && [ -n "$TRACK_1001" ] || fail "INV-1001 missing tracking_id"

wait_for_submission_status "$TRACK_1001" "PAID" "INV-1001"
wait_for_audit_actions "$CORR_1001" \
  "submission_accepted" \
  "decision_produced" \
  "payment_requested_published" \
  "payment_succeeded"

log "Scenario: INV-1002 second auto approve -> PAID"
CORR_1002="$RUN_ID-1002"
RESP_1002="$(submit_invoice "INV-1002" "$CORR_1002" "202")"
TRACK_1002="$(printf "%s" "$RESP_1002" | jq -r '.tracking_id')"

[ "$TRACK_1002" != "null" ] && [ -n "$TRACK_1002" ] || fail "INV-1002 missing tracking_id"

wait_for_submission_status "$TRACK_1002" "PAID" "INV-1002"
wait_for_audit_actions "$CORR_1002" \
  "submission_accepted" \
  "decision_produced" \
  "payment_requested_published" \
  "payment_succeeded"

log "Scenario: INV-1003 human review -> approve -> PAID"
CORR_1003="$RUN_ID-1003"
RESP_1003="$(submit_invoice "INV-1003" "$CORR_1003" "202")"
TRACK_1003="$(printf "%s" "$RESP_1003" | jq -r '.tracking_id')"

[ "$TRACK_1003" != "null" ] && [ -n "$TRACK_1003" ] || fail "INV-1003 missing tracking_id"

wait_for_approval_pending "$TRACK_1003" "INV-1003"
approve_submission "$TRACK_1003" "$RUN_ID-approve-1003"
wait_for_submission_status "$TRACK_1003" "PAID" "INV-1003"
wait_for_audit_actions "$CORR_1003" \
  "submission_accepted" \
  "approval_required_published" \
  "decision_produced" \
  "approval_item_queued" \
  "human_approved" \
  "payment_succeeded"

log "Scenario: INV-1007 duplicate of INV-1001 -> no second processing"
CORR_1007="$RUN_ID-1007"
RESP_1007="$(submit_invoice "INV-1007" "$CORR_1007" "200")"

DUPLICATE_FLAG="$(printf "%s" "$RESP_1007" | jq -r '.duplicate')"
DUPLICATE_OF="$(printf "%s" "$RESP_1007" | jq -r '.duplicate_of')"
DUP_TRACKING="$(printf "%s" "$RESP_1007" | jq -r '.tracking_id')"

[ "$DUPLICATE_FLAG" = "true" ] || fail "INV-1007 duplicate flag was not true"
[ "$DUPLICATE_OF" = "$TRACK_1001" ] || fail "INV-1007 duplicate_of expected $TRACK_1001 but got $DUPLICATE_OF"
[ "$DUP_TRACKING" = "$TRACK_1001" ] || fail "INV-1007 tracking_id expected existing $TRACK_1001 but got $DUP_TRACKING"

pass "INV-1007 duplicate returned existing INV-1001 tracking id"
wait_for_audit_actions "$CORR_1007" "duplicate_submission_detected"
assert_no_audit_action "$CORR_1007" "payment_succeeded"

log "Scenario: INV-1012 human approve -> payment failure -> compensation"
CORR_1012="$RUN_ID-1012"
RESP_1012="$(submit_invoice "INV-1012" "$CORR_1012" "202")"
TRACK_1012="$(printf "%s" "$RESP_1012" | jq -r '.tracking_id')"

[ "$TRACK_1012" != "null" ] && [ -n "$TRACK_1012" ] || fail "INV-1012 missing tracking_id"

wait_for_approval_pending "$TRACK_1012" "INV-1012"
approve_submission "$TRACK_1012" "$RUN_ID-approve-1012"
wait_for_submission_status "$TRACK_1012" "PAYMENT_FAILED" "INV-1012"
wait_for_audit_actions "$CORR_1012" \
  "submission_accepted" \
  "approval_required_published" \
  "decision_produced" \
  "approval_item_queued" \
  "human_approved" \
  "payment_failed_compensated"

log "Scenario: cumulative autonomy budget blocks split-invoice bypass"
CORR_1020="$RUN_ID-1020"
RESP_1020="$(submit_invoice "INV-1020" "$CORR_1020" "202")"
TRACK_1020="$(printf "%s" "$RESP_1020" | jq -r '.tracking_id')"

[ "$TRACK_1020" != "null" ] && [ -n "$TRACK_1020" ] || fail "INV-1020 missing tracking_id"

wait_for_submission_status "$TRACK_1020" "PAID" "INV-1020"
wait_for_audit_actions "$CORR_1020" \
  "submission_accepted" \
  "decision_produced" \
  "payment_requested_published" \
  "payment_succeeded"

CORR_1021="$RUN_ID-1021"
RESP_1021="$(submit_invoice "INV-1021" "$CORR_1021" "202")"
TRACK_1021="$(printf "%s" "$RESP_1021" | jq -r '.tracking_id')"

[ "$TRACK_1021" != "null" ] && [ -n "$TRACK_1021" ] || fail "INV-1021 missing tracking_id"

wait_for_submission_status "$TRACK_1021" "HUMAN_REVIEW_REQUIRED" "INV-1021"
wait_for_approval_pending "$TRACK_1021" "INV-1021"
assert_approval_violation "$TRACK_1021" "AUTONOMY-BUDGET" "INV-1021"
wait_for_audit_actions "$CORR_1021" \
  "submission_accepted" \
  "decision_produced" \
  "approval_required_published" \
  "approval_item_queued"

log "Prompt-injection: INV-1013 prompt injection cannot auto-approve"
CORR_1013="$RUN_ID-1013"
RESP_1013="$(submit_invoice "INV-1013" "$CORR_1013" "202")"
TRACK_1013="$(printf "%s" "$RESP_1013" | jq -r '.tracking_id')"

[ "$TRACK_1013" != "null" ] && [ -n "$TRACK_1013" ] || fail "INV-1013 missing tracking_id"

wait_for_submission_status "$TRACK_1013" "HUMAN_REVIEW_REQUIRED" "INV-1013"
assert_anti_cheese_approval "$TRACK_1013"
wait_for_audit_actions "$CORR_1013" \
  "submission_accepted" \
  "approval_required_published" \
  "decision_produced" \
  "approval_item_queued"

log "Verification summary"
printf "PASS count: %d\n" "$PASSED"
printf "FAIL count: %d\n" "$FAILED"

if [ "$FAILED" -eq 0 ]; then
  printf "\nALL VERIFICATION CHECKS PASSED\n"
fi
