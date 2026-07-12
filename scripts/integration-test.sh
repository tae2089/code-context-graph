#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="docker-compose.integration.yml"
COMPOSE_CMD=${COMPOSE_CMD:-"docker compose"}
GITEA_URL="http://localhost:3000"
CCG_URL="http://localhost:18080"
ADMIN_USER="testadmin"
ADMIN_PASS="testadmin1234"
ADMIN_EMAIL="admin@test.local"
WEBHOOK_SECRET="test-webhook-secret"
MCP_BEARER_TOKEN="test-mcp-token"
REPO_NAME="sample-go"
REPO2_NAME="sample-calc"
TMPDIR_CLONE=""
TMPDIR_CLONE2=""
KEEP_CONTAINERS=${KEEP_CONTAINERS:-0}
ARTIFACT_DIR=${ARTIFACT_DIR:-"artifacts/integration-$(date +%Y%m%d-%H%M%S)"}
DUMP_ON_SUCCESS=${DUMP_ON_SUCCESS:-0}
WEBHOOK_WAIT_SECONDS=${WEBHOOK_WAIT_SECONDS:-60}
CCG_E2E_ALLOW_MCP_LOG_FALLBACK=${CCG_E2E_ALLOW_MCP_LOG_FALLBACK:-0}

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail()  { echo -e "${RED}[FAIL]${NC} $*"; exit 1; }

compose() {
    local -a cmd
    # shellcheck disable=SC2206
    cmd=( $COMPOSE_CMD )
    "${cmd[@]}" -f "$COMPOSE_FILE" "$@"
}

prepare_artifact_dir() {
    mkdir -p "$ARTIFACT_DIR"
}

dump_text_artifact() {
    local name="$1" content="$2"
    prepare_artifact_dir
    printf '%s\n' "$content" > "${ARTIFACT_DIR}/${name}"
}

capture_artifacts() {
    local reason="$1"
    prepare_artifact_dir
    info "Capturing integration artifacts (${reason}) in ${ARTIFACT_DIR}"
    compose ps > "${ARTIFACT_DIR}/compose-ps.txt" 2>&1 || true
    compose logs --no-color > "${ARTIFACT_DIR}/compose.log" 2>&1 || true
    compose logs --no-color ccg > "${ARTIFACT_DIR}/ccg.log" 2>&1 || true
    compose logs --no-color gitea > "${ARTIFACT_DIR}/gitea.log" 2>&1 || true
    compose logs --no-color postgres > "${ARTIFACT_DIR}/postgres.log" 2>&1 || true
}

cleanup() {
    local status=$?
    if [ "$status" -ne 0 ] || [ "$DUMP_ON_SUCCESS" = "1" ]; then
        capture_artifacts "$([ "$status" -eq 0 ] && printf success || printf failure)"
    fi
    info "Cleaning up..."
    [ -n "$TMPDIR_CLONE" ] && rm -rf "$TMPDIR_CLONE"
    [ -n "$TMPDIR_CLONE2" ] && rm -rf "$TMPDIR_CLONE2"
    if [ "$KEEP_CONTAINERS" = "1" ]; then
        warn "KEEP_CONTAINERS=1 set; leaving Docker containers running"
        return "$status"
    fi
    compose down -v --remove-orphans 2>/dev/null || true
    return "$status"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    trap cleanup EXIT
fi

wait_for_health() {
    local url="$1" max="$2" label="$3"
    for i in $(seq 1 "$max"); do
        if curl -fsS "$url" >/dev/null 2>&1; then
            info "$label is healthy"
            return 0
        fi
        echo -n "."
        sleep 2
    done
    fail "$label did not become healthy after $((max * 2))s"
}

wait_for_postgres() {
    local max="$1" label="${2:-PostgreSQL}"
    for i in $(seq 1 "$max"); do
        if compose exec -T postgres pg_isready -U ccg -d ccg_integration >/dev/null 2>&1; then
            info "$label is healthy"
            return 0
        fi
        echo -n "."
        sleep 2
    done
    fail "$label did not become healthy after $((max * 2))s"
}

start_integration_stack() {
    info "Starting base services..."
    compose up -d postgres gitea

    info "Building ccg image..."
    compose build ccg

    info "Waiting for PostgreSQL..."
    wait_for_postgres 30 "PostgreSQL"

    info "Waiting for Gitea..."
    wait_for_health "$GITEA_URL/api/v1/version" 30 "Gitea"

    info "Running ccg migrations..."
    compose run --rm --no-deps --entrypoint ccg ccg migrate

    info "Starting ccg..."
    compose up -d ccg

    info "Waiting for ccg..."
    wait_for_health "$CCG_URL/ready" 30 "ccg"
}

api() {
    local method="$1" path="$2"; shift 2
    curl -fsS -X "$method" "${GITEA_URL}${path}" \
        -H "Content-Type: application/json" \
        -H "Authorization: token ${GITEA_TOKEN:-}" \
        "$@"
}

# MCP tool call helper — returns JSON response
MCP_REQ_ID=100
mcp_call() {
    local tool="$1"; shift
    local args="$1"
    MCP_REQ_ID=$((MCP_REQ_ID + 1))
    local resp
    resp=$(curl -sS -X POST "${CCG_URL}/mcp" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${MCP_BEARER_TOKEN}" \
        -H "Mcp-Session-Id: ${MCP_SESSION}" \
        -d "{\"jsonrpc\":\"2.0\",\"id\":${MCP_REQ_ID},\"method\":\"tools/call\",\"params\":{\"name\":\"${tool}\",\"arguments\":${args}}}" 2>/dev/null) || resp=""
    echo "$resp"
}

# Extract text content from MCP response
mcp_text() {
    local resp="$1"
    printf '%s' "$resp" | python3 -c '
import json, sys
r = json.load(sys.stdin)
content = r["result"]["content"]
if not isinstance(content, list) or not content:
    raise KeyError("missing result.content[0]")
text = content[0]["text"]
if not isinstance(text, str):
    raise TypeError("result.content[0].text must be a string")
print(text)
' 2>/dev/null
}

# Assert MCP response is not an error
assert_mcp_ok() {
    local tool="$1" resp="$2"
    if [ -z "$resp" ]; then
        fail "❌ ${tool}: empty response"
    fi
    local validation_error
    validation_error=$(printf '%s' "$resp" | python3 -c '
import json, sys
try:
    r = json.load(sys.stdin)
except Exception as exc:
    print(f"malformed JSON: {exc}", file=sys.stderr)
    sys.exit(2)
if not isinstance(r, dict):
    print("response is not a JSON object", file=sys.stderr)
    sys.exit(2)
if r.get("error") is not None:
    print("JSON-RPC error:", r.get("error"), file=sys.stderr)
    sys.exit(3)
result = r.get("result")
if not isinstance(result, dict):
    print("missing result object", file=sys.stderr)
    sys.exit(4)
if result.get("isError") is True:
    text = ""
    content = result.get("content")
    if isinstance(content, list) and content and isinstance(content[0], dict):
        text = str(content[0].get("text", ""))
    print(f"MCP result.isError=true: {text}", file=sys.stderr)
    sys.exit(5)
' 2>&1 >/dev/null) || fail "❌ ${tool}: ${validation_error}"
    info "✅ ${tool}: OK"
}

# Assert MCP text response contains a string
assert_mcp_contains() {
    local tool="$1" resp="$2" expected="$3"
    local text
    assert_mcp_ok "$tool" "$resp"
    text=$(mcp_text "$resp") || fail "❌ ${tool}: missing result.content[0].text"
    if [[ "$text" == *"$expected"* ]]; then
        info "✅ ${tool}: contains '${expected}'"
    else
        fail "❌ ${tool}: expected '${expected}' in response, got: ${text:0:200}"
    fi
}

assert_mcp_not_contains() {
    local tool="$1" resp="$2" unexpected="$3"
    local text
    assert_mcp_ok "$tool" "$resp"
    text=$(mcp_text "$resp") || fail "❌ ${tool}: missing result.content[0].text"
    if [[ "$text" == *"$unexpected"* ]]; then
        fail "❌ ${tool}: unexpectedly contains '${unexpected}'"
    fi
    info "✅ ${tool}: does not contain '${unexpected}'"
}

assert_mcp_error() {
    local tool="$1" resp="$2" expected="${3:-}"
    local text
    if [ -z "$resp" ]; then
        fail "❌ ${tool}: empty response"
    fi
    text=$(printf '%s' "$resp" | python3 -c '
import json, sys
try:
    response = json.load(sys.stdin)
except Exception:
    sys.exit(2)
result = response.get("result") if isinstance(response, dict) else None
if not isinstance(result, dict) or result.get("isError") is not True:
    sys.exit(3)
content = result.get("content")
if not isinstance(content, list) or not content or not isinstance(content[0], dict):
    sys.exit(4)
text = content[0].get("text")
if not isinstance(text, str):
    sys.exit(5)
print(text)
' 2>/dev/null) || fail "❌ ${tool}: expected MCP tool error"
    if [ -n "$expected" ] && [[ "$text" != *"$expected"* ]]; then
        fail "❌ ${tool}: expected error containing '${expected}'"
    fi
    info "✅ ${tool}: returned expected error"
}

# Assert JSON field in MCP text > 0
assert_mcp_gt0() {
    local tool="$1" resp="$2" field="$3"
    local val
    assert_mcp_ok "$tool" "$resp"
    val=$(printf '%s' "$resp" | FIELD="$field" python3 -c '
import json, os, sys
r = json.load(sys.stdin)
d = json.loads(r["result"]["content"][0]["text"])
print(d.get(os.environ["FIELD"], 0))
' 2>/dev/null) || val=0
    if [ "${val:-0}" -gt 0 ] 2>/dev/null; then
        info "✅ ${tool}: ${field}=${val}"
    else
        fail "❌ ${tool}: expected ${field} > 0, got ${val}"
    fi
}

graph_nodes_for_namespace() {
    local namespace="$1" resp total
    [ -n "${MCP_SESSION:-}" ] || return 1
    resp=$(mcp_call "list_graph_stats" "{\"namespace\":\"${namespace}\"}")
    [ -n "$resp" ] || return 1
    total=$(echo "$resp" | python3 -c "import sys,json; r=json.load(sys.stdin); d=json.loads(r['result']['content'][0]['text']); print(d.get('total_nodes',0))" 2>/dev/null) || total=0
    [ "${total:-0}" -gt 0 ] 2>/dev/null
}

extract_mcp_session_id() {
    local init_resp="$1" line key lower_key value
    while IFS= read -r line; do
        line=${line%$'\r'}
        key=${line%%:*}
        lower_key=$(printf '%s' "$key" | tr '[:upper:]' '[:lower:]')
        if [ "$lower_key" = "mcp-session-id" ]; then
            value=${line#*:}
            value="${value#"${value%%[![:space:]]*}"}"
            value="${value%"${value##*[![:space:]]}"}"
            printf '%s\n' "$value"
            return 0
        fi
    done <<< "$init_resp"
}

mcp_session_required() {
    local session="$1"
    [ -n "$session" ] || [ "${CCG_E2E_ALLOW_MCP_LOG_FALLBACK:-0}" = "1" ]
}

verify_wiki_endpoint() {
    info "Checking Wiki endpoint..."
    curl -fsS "${CCG_URL}/wiki/" >/dev/null || fail "Wiki UI did not respond"

    local code
    code=$(curl -sS -o /dev/null -w '%{http_code}' "${CCG_URL}/wiki/api/namespaces")
    [ "$code" = "401" ] || fail "Expected unauthenticated Wiki API to return 401, got ${code}"

    curl -fsS "${CCG_URL}/wiki/api/namespaces" \
        -H "Authorization: Bearer ${MCP_BEARER_TOKEN}" >/dev/null || fail "Authenticated Wiki API failed"
    info "Wiki endpoint ready"
}

mcp_log_fallback_allowed() {
    [ "${CCG_E2E_ALLOW_MCP_LOG_FALLBACK:-0}" = "1" ]
}

extract_last_webhook_node_count() {
    local logs="$1"
    printf '%s' "$logs" | python3 -c '
import re, sys
matches = re.findall(r"webhook sync completed[^\n]*nodes=(\d+)", sys.stdin.read())
print(matches[-1] if matches else "0")
'
}

webhook_logs_completed() {
    local logs="$1" namespace="$2"
    if [ -n "$namespace" ]; then
        [[ "$logs" == *"webhook sync completed"*"$namespace"* ]] && return 0
        [ "$namespace" = "$REPO_NAME" ] && [[ "$logs" == *"webhook sync completed"* ]]
    else
        [[ "$logs" == *"webhook sync completed"* ]]
    fi
}

webhook_logs_failed() {
    local logs="$1" namespace="$2"
    if [ -n "$namespace" ]; then
        [[ "$logs" == *"webhook build failed"*"$namespace"* ]] && return 0
        [ "$namespace" = "$REPO_NAME" ] && [[ "$logs" == *"webhook build failed"* ]]
    else
        [[ "$logs" == *"webhook build failed"* ]]
    fi
}

wait_for_webhook_sync() {
    local namespace="$1" max_seconds="${2:-$WEBHOOK_WAIT_SECONDS}" label="${3:-$namespace}"
    local deadline=$((SECONDS + max_seconds)) logs
    while [ $SECONDS -lt $deadline ]; do
        if graph_nodes_for_namespace "$namespace"; then
            info "${label} webhook sync observable via MCP"
            return 0
        fi

        logs=$(compose logs ccg 2>/dev/null || true)
        if webhook_logs_failed "$logs" "$namespace"; then
            fail "ccg build failed for ${label}. See ${ARTIFACT_DIR}/ccg.log after cleanup."
        fi
        if mcp_log_fallback_allowed && webhook_logs_completed "$logs" "$namespace"; then
            info "${label} webhook sync observed via logs"
            return 0
        fi
        sleep 2
    done
    fail "Timed out waiting for ${label} webhook sync"
}

run_integration_test() {

# ── Phase 1: Start containers ──
 info "Starting containers..."
 start_integration_stack
 verify_wiki_endpoint

# ── Phase 2: Create Gitea admin user ──
info "Creating Gitea admin user..."
compose exec -T gitea \
    gitea admin user create \
        --username "$ADMIN_USER" \
        --password "$ADMIN_PASS" \
        --email "$ADMIN_EMAIL" \
        --admin \
        --must-change-password=false 2>/dev/null || warn "Admin user may already exist"

info "Creating API token..."
TOKEN_RESP=$(curl -fsS -X POST "${GITEA_URL}/api/v1/users/${ADMIN_USER}/tokens" \
    -u "${ADMIN_USER}:${ADMIN_PASS}" \
    -H "Content-Type: application/json" \
    -d '{"name":"integration-test","scopes":["all"]}')
GITEA_TOKEN=$(echo "$TOKEN_RESP" | grep -o '"sha1":"[^"]*"' | cut -d'"' -f4)
[ -z "$GITEA_TOKEN" ] && fail "Failed to create API token"
info "Token acquired"

# ── Phase 3: Create repository with sample Go code ──
info "Creating repository: ${REPO_NAME}..."
api POST "/api/v1/user/repos" \
    -d "{\"name\":\"${REPO_NAME}\",\"auto_init\":true,\"default_branch\":\"main\"}" >/dev/null

# ── Phase 4: Register webhook ──
info "Registering webhook on ccg server..."
HOOK_RESP=$(api POST "/api/v1/repos/${ADMIN_USER}/${REPO_NAME}/hooks" \
    -d "{
        \"type\":\"gitea\",
        \"config\":{
            \"url\":\"http://ccg:8080/webhook\",
            \"content_type\":\"json\",
            \"secret\":\"${WEBHOOK_SECRET}\"
        },
        \"events\":[\"push\"],
        \"active\":true
    }")
HOOK_ID=$(echo "$HOOK_RESP" | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)
[ -z "$HOOK_ID" ] && fail "Failed to create webhook"
info "Webhook created: id=${HOOK_ID}"

# ── Phase 5: Clone repo, add Go source, push ──
TMPDIR_CLONE=$(mktemp -d)
info "Cloning repo to ${TMPDIR_CLONE}..."
git clone -q "http://${ADMIN_USER}:${ADMIN_PASS}@localhost:3000/${ADMIN_USER}/${REPO_NAME}.git" "${TMPDIR_CLONE}/repo"

cat > "${TMPDIR_CLONE}/repo/main.go" <<'GOEOF'
package main

import "fmt"

func greet(name string) string {
	return fmt.Sprintf("Hello, %s!", name)
}

func main() {
	fmt.Println(greet("world"))
}
GOEOF

cat > "${TMPDIR_CLONE}/repo/calc.go" <<'GOEOF'
package main

func add(a, b int) int {
	return a + b
}

func multiply(a, b int) int {
	return a * b
}
GOEOF

pushd "${TMPDIR_CLONE}/repo" >/dev/null
git config user.email "$ADMIN_EMAIL"
git config user.name "$ADMIN_USER"
git add -A
git commit -q -m "feat: add sample Go code"
info "Pushing to Gitea (triggers webhook)..."
git push -q origin main
popd >/dev/null

# ── Phase 6: Wait for ccg to process ──
info "Waiting for webhook sync + build (max ${WEBHOOK_WAIT_SECONDS}s)..."

# ── Phase 7: Verify graph data via MCP (initialize → tools/call) ──
info "Initializing MCP session..."
INIT_RESP=$(curl -sS -D - -X POST "${CCG_URL}/mcp" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${MCP_BEARER_TOKEN}" \
    -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"integration-test","version":"1.0.0"}}}' 2>/dev/null) || INIT_RESP=""

MCP_SESSION=$(extract_mcp_session_id "$INIT_RESP")

if [ -z "$MCP_SESSION" ]; then
    if ! mcp_session_required "$MCP_SESSION"; then
        fail "MCP session init failed; refusing log-only false green (set CCG_E2E_ALLOW_MCP_LOG_FALLBACK=1 for local debugging only)"
    fi
    warn "MCP session init failed — using log-based webhook smoke check only because CCG_E2E_ALLOW_MCP_LOG_FALLBACK=1"
    wait_for_webhook_sync "$REPO_NAME" "$WEBHOOK_WAIT_SECONDS" "$REPO_NAME"
    LOGS=$(compose logs ccg 2>/dev/null)
    NODE_COUNT=$(extract_last_webhook_node_count "$LOGS")
    info "Build reported nodes=${NODE_COUNT:-0}"
    [ "${NODE_COUNT:-0}" -gt 0 ] || fail "Expected nodes > 0"
else
    info "MCP session initialized"
    wait_for_webhook_sync "$REPO_NAME" "$WEBHOOK_WAIT_SECONDS" "$REPO_NAME"
    STATS_RESP=$(mcp_call "list_graph_stats" "{\"namespace\":\"${REPO_NAME}\"}")

    if [ -z "$STATS_RESP" ]; then
        fail "MCP tools/call returned empty response"
    fi

    info "MCP list_graph_stats response:"
    echo "$STATS_RESP" | python3 -m json.tool 2>/dev/null || echo "$STATS_RESP"

    assert_mcp_ok "list_graph_stats" "$STATS_RESP"
    TOTAL_NODES=$(printf '%s' "$STATS_RESP" | python3 -c "import sys,json; r=json.load(sys.stdin); d=json.loads(r['result']['content'][0]['text']); print(d.get('total_nodes',0))" 2>/dev/null) || TOTAL_NODES=0
    [ "${TOTAL_NODES:-0}" -gt 0 ] || fail "Expected total_nodes > 0, got ${TOTAL_NODES:-0}"
    info "Verified via MCP: total_nodes=${TOTAL_NODES}"
fi

# ── Phase 8: Namespace isolation test (second repo) ──
info "Creating second repository: ${REPO2_NAME}..."
api POST "/api/v1/user/repos" \
    -d "{\"name\":\"${REPO2_NAME}\",\"auto_init\":true,\"default_branch\":\"main\"}" >/dev/null

info "Registering webhook for ${REPO2_NAME}..."
HOOK2_RESP=$(api POST "/api/v1/repos/${ADMIN_USER}/${REPO2_NAME}/hooks" \
    -d "{
        \"type\":\"gitea\",
        \"config\":{
            \"url\":\"http://ccg:8080/webhook\",
            \"content_type\":\"json\",
            \"secret\":\"${WEBHOOK_SECRET}\"
        },
        \"events\":[\"push\"],
        \"active\":true
    }")
HOOK2_ID=$(echo "$HOOK2_RESP" | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)
[ -z "$HOOK2_ID" ] && fail "Failed to create webhook for ${REPO2_NAME}"
info "Webhook created for ${REPO2_NAME}: id=${HOOK2_ID}"

TMPDIR_CLONE2=$(mktemp -d)
info "Cloning ${REPO2_NAME} to ${TMPDIR_CLONE2}..."
git clone -q "http://${ADMIN_USER}:${ADMIN_PASS}@localhost:3000/${ADMIN_USER}/${REPO2_NAME}.git" "${TMPDIR_CLONE2}/repo"

cat > "${TMPDIR_CLONE2}/repo/math.go" <<'GOEOF'
package main

import "fmt"

// @intent 두 수의 차이를 계산한다
func subtract(a, b int) int {
	return a - b
}

// @intent 두 수의 나눗셈을 수행한다
func divide(a, b int) (int, error) {
	if b == 0 {
		return 0, fmt.Errorf("division by zero")
	}
	return a / b, nil
}

func main() {
	fmt.Println(subtract(10, 3))
}
GOEOF

pushd "${TMPDIR_CLONE2}/repo" >/dev/null
git config user.email "$ADMIN_EMAIL"
git config user.name "$ADMIN_USER"
git add -A
git commit -q -m "feat: add math functions"
info "Pushing ${REPO2_NAME} to Gitea (triggers webhook)..."
git push -q origin main
popd >/dev/null

info "Waiting for ${REPO2_NAME} webhook sync (max ${WEBHOOK_WAIT_SECONDS}s)..."
wait_for_webhook_sync "$REPO2_NAME" "$WEBHOOK_WAIT_SECONDS" "$REPO2_NAME"
info "${REPO2_NAME} sync completed"

# Namespace isolation verification via supported MCP graph tools
if [ -n "$MCP_SESSION" ]; then
    info "Running postprocess for namespace=${REPO_NAME}..."
    PP1_RESP=$(mcp_call "run_postprocess" "{\"namespace\":\"${REPO_NAME}\",\"flows\":true,\"fts\":true}")
    assert_mcp_ok "run_postprocess(${REPO_NAME})" "$PP1_RESP"

    info "Running postprocess for namespace=${REPO2_NAME}..."
    PP2_RESP=$(mcp_call "run_postprocess" "{\"namespace\":\"${REPO2_NAME}\",\"flows\":true,\"fts\":true}")
    assert_mcp_ok "run_postprocess(${REPO2_NAME})" "$PP2_RESP"

    info "Checking graph stats per namespace..."
    STATS1=$(mcp_call "list_graph_stats" "{\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_gt0 "list_graph_stats(${REPO_NAME})" "$STATS1" "total_nodes"
    STATS2=$(mcp_call "list_graph_stats" "{\"namespace\":\"${REPO2_NAME}\"}")
    assert_mcp_gt0 "list_graph_stats(${REPO2_NAME})" "$STATS2" "total_nodes"

    info "Namespace graph counts verified"
else
    warn "MCP session unavailable under local debug override — skipping namespace isolation test"
fi

# ── Phase 9: Check webhook delivery status (best-effort) ──
info "Checking webhook delivery history..."
DELIVERIES=$(api GET "/api/v1/repos/${ADMIN_USER}/${REPO_NAME}/hooks/${HOOK_ID}/deliveries" 2>/dev/null) || DELIVERIES="[]"
DELIVERY_COUNT=$(echo "$DELIVERIES" | grep -c '"id"' 2>/dev/null) || DELIVERY_COUNT=0
info "Webhook deliveries: ${DELIVERY_COUNT}"

# ── Phase 10: Graph Query tools ──
if [ -n "$MCP_SESSION" ]; then
    info "Phase 10: Testing Graph Query tools..."

    # get_node — lookup greet function from sample-go
    RESP=$(mcp_call "get_node" "{\"qualified_name\":\"main.greet\",\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_ok "get_node" "$RESP"
    assert_mcp_contains "get_node" "$RESP" "greet"

    # search — full-text search for "greet"
    RESP=$(mcp_call "search" "{\"query\":\"greet\",\"limit\":5,\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_ok "search" "$RESP"
    assert_mcp_contains "search" "$RESP" "greet"

    RESP=$(mcp_call "search" "{\"query\":\"greet\",\"limit\":5,\"namespace\":\"${REPO2_NAME}\"}")
    assert_mcp_not_contains "search(${REPO2_NAME})" "$RESP" "greet"

    RESP=$(mcp_call "search" "{\"query\":\"subtract\",\"limit\":5,\"namespace\":\"${REPO2_NAME}\"}")
    assert_mcp_contains "search(${REPO2_NAME})" "$RESP" "subtract"

    RESP=$(mcp_call "search" "{\"query\":\"subtract\",\"limit\":5,\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_not_contains "search(${REPO_NAME})" "$RESP" "subtract"

    # get_annotation — subtract has @intent tag in sample-calc
    RESP=$(mcp_call "get_annotation" "{\"qualified_name\":\"main.subtract\",\"namespace\":\"${REPO2_NAME}\"}")
    assert_mcp_ok "get_annotation" "$RESP"
    assert_mcp_contains "get_annotation" "$RESP" "intent"

    # query_graph — file_summary for main.go
    RESP=$(mcp_call "query_graph" "{\"pattern\":\"file_summary\",\"target\":\"main.go\",\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_ok "query_graph(file_summary)" "$RESP"
    assert_mcp_contains "query_graph(file_summary)" "$RESP" "file_summary"

    # query_graph — callees_of main.main
    RESP=$(mcp_call "query_graph" "{\"pattern\":\"callees_of\",\"target\":\"main.main\",\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_ok "query_graph(callees_of)" "$RESP"

    info "Phase 10 complete ✅"
else
    warn "MCP session unavailable under local debug override — skipping Phase 10"
fi

# ── Phase 11: Analysis tools ──
if [ -n "$MCP_SESSION" ]; then
    info "Phase 11: Testing Analysis tools..."

    # get_impact_radius — greet function blast radius
    RESP=$(mcp_call "get_impact_radius" "{\"qualified_name\":\"main.greet\",\"depth\":1,\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_ok "get_impact_radius" "$RESP"

    # trace_flow — main.main flow trace
    RESP=$(mcp_call "trace_flow" "{\"qualified_name\":\"main.main\",\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_ok "trace_flow" "$RESP"

    RESP=$(mcp_call "detect_changes" "{\"repo_root\":\"/repos/${REPO_NAME}\",\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_ok "detect_changes" "$RESP"

    RESP=$(mcp_call "get_affected_flows" "{\"repo_root\":\"/repos/${REPO_NAME}\",\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_ok "get_affected_flows" "$RESP"

    # get_minimal_context — task-based context
    RESP=$(mcp_call "get_minimal_context" "{\"task\":\"review\",\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_ok "get_minimal_context" "$RESP"
    assert_mcp_contains "get_minimal_context" "$RESP" "summary"

    info "Phase 11 complete ✅"
else
    warn "MCP session unavailable under local debug override — skipping Phase 11"
fi

# ── Phase 12: Graph structure and namespace tools ──
if [ -n "$MCP_SESSION" ]; then
    info "Phase 12: Testing graph structure and namespace tools..."

    # list_flows
    RESP=$(mcp_call "list_flows" "{\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_ok "list_flows" "$RESP"
    assert_mcp_contains "list_flows" "$RESP" "flows"

    # list_namespaces
    RESP=$(mcp_call "list_namespaces" "{}")
    assert_mcp_ok "list_namespaces" "$RESP"
    assert_mcp_contains "list_namespaces" "$RESP" "$REPO_NAME"
    assert_mcp_contains "list_namespaces" "$RESP" "$REPO2_NAME"

    info "Phase 12 complete ✅"
else
    warn "MCP session unavailable under local debug override — skipping Phase 12"
fi

# ── Phase 14: Documentation discovery tools ──
if [ -n "$MCP_SESSION" ]; then
    info "Phase 14: Testing documentation discovery tools..."

    # search_docs is DB-backed and does not require a generated retrieval index.
    RESP=$(mcp_call "search_docs" "{\"query\":\"greet\",\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_ok "search_docs" "$RESP"

    # This webhook E2E does not generate Markdown, so verify the documented missing-file contract.
    RESP=$(mcp_call "get_doc_content" "{\"file_path\":\"docs/nonexistent.md\",\"namespace\":\"${REPO_NAME}\"}")
    assert_mcp_error "get_doc_content" "$RESP"

    info "Phase 14 complete ✅"
else
    warn "MCP session unavailable under local debug override — skipping Phase 14"
fi

# Note: the former Phase 15 (MCP parse_project/build_or_update_graph via uploaded
# namespace files) was removed with the file-upload tools. Graph build is covered
# by the webhook clone→build pipeline earlier in this script.

# ── Summary ──
TOOLS_TESTED=0
TOTAL_MCP_TOOLS=17
if [ -n "$MCP_SESSION" ]; then
    TOOLS_TESTED=15
fi

echo ""
echo -e "${GREEN}════════════════════════════════════════════════════════${NC}"
if [ -n "$MCP_SESSION" ]; then
    echo -e "${GREEN}  Integration test PASSED — ${TOOLS_TESTED}/${TOTAL_MCP_TOOLS} MCP tools verified${NC}"
else
    echo -e "${YELLOW}  Integration webhook smoke completed — 0/${TOTAL_MCP_TOOLS} MCP tools verified (local debug override)${NC}"
fi
echo -e "${GREEN}════════════════════════════════════════════════════════${NC}"
echo ""
info "Pipeline: Gitea push → webhook → ccg clone → build → DB ✅"
info "MCP tools: graph query, analysis, namespace, documentation discovery, postprocess ✅"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    run_integration_test "$@"
fi
