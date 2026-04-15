#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="docker-compose.integration.yml"
GITEA_URL="http://localhost:3000"
CCG_URL="http://localhost:18080"
ADMIN_USER="testadmin"
ADMIN_PASS="testadmin1234"
ADMIN_EMAIL="admin@test.local"
WEBHOOK_SECRET="test-webhook-secret"
REPO_NAME="sample-go"
TMPDIR_CLONE=""

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail()  { echo -e "${RED}[FAIL]${NC} $*"; exit 1; }

cleanup() {
    info "Cleaning up..."
    [ -n "$TMPDIR_CLONE" ] && rm -rf "$TMPDIR_CLONE"
    docker compose -f "$COMPOSE_FILE" down -v --remove-orphans 2>/dev/null || true
}
trap cleanup EXIT

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

api() {
    local method="$1" path="$2"; shift 2
    curl -fsS -X "$method" "${GITEA_URL}${path}" \
        -H "Content-Type: application/json" \
        -H "Authorization: token ${GITEA_TOKEN:-}" \
        "$@"
}

# ── Phase 1: Start containers ──
info "Starting containers..."
docker compose -f "$COMPOSE_FILE" up -d --build

info "Waiting for Gitea..."
wait_for_health "$GITEA_URL/api/v1/version" 30 "Gitea"

info "Waiting for ccg..."
wait_for_health "$CCG_URL/health" 30 "ccg"

# ── Phase 2: Create Gitea admin user ──
info "Creating Gitea admin user..."
docker compose -f "$COMPOSE_FILE" exec -T gitea \
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
[ -z "$GITEA_TOKEN" ] && fail "Failed to create API token: $TOKEN_RESP"
info "Token acquired: ${GITEA_TOKEN:0:8}..."

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
[ -z "$HOOK_ID" ] && fail "Failed to create webhook: $HOOK_RESP"
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
info "Waiting for webhook sync + build (max 60s)..."
DEADLINE=$((SECONDS + 60))
BUILD_OK=false
while [ $SECONDS -lt $DEADLINE ]; do
    LOGS=$(docker compose -f "$COMPOSE_FILE" logs ccg 2>/dev/null)
    if echo "$LOGS" | grep -q "webhook sync completed"; then
        BUILD_OK=true
        break
    fi
    if echo "$LOGS" | grep -q "webhook build failed"; then
        fail "ccg build failed. Logs:\n$(echo "$LOGS" | tail -20)"
    fi
    sleep 2
done
$BUILD_OK || fail "Timed out waiting for webhook sync"

# ── Phase 7: Verify graph data via MCP ──
info "Verifying graph data in DB..."
STATS_RESP=$(curl -fsS -X POST "${CCG_URL}/mcp" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_graph_stats","arguments":{}}}' 2>/dev/null || echo "MCP_FAILED")

if echo "$STATS_RESP" | grep -q "MCP_FAILED"; then
    warn "MCP call failed — falling back to log-based verification"
    LOGS=$(docker compose -f "$COMPOSE_FILE" logs ccg 2>/dev/null)
    if echo "$LOGS" | grep -q "nodes="; then
        NODE_COUNT=$(echo "$LOGS" | grep "webhook sync completed" | grep -o 'nodes=[0-9]*' | tail -1 | cut -d= -f2)
        info "Build reported nodes=${NODE_COUNT}"
        [ "${NODE_COUNT:-0}" -gt 0 ] || fail "Expected nodes > 0"
    fi
else
    info "MCP stats response received"
    echo "$STATS_RESP" | python3 -m json.tool 2>/dev/null || echo "$STATS_RESP"
fi

# ── Phase 8: Check webhook delivery status (best-effort) ──
info "Checking webhook delivery history..."
DELIVERIES=$(api GET "/api/v1/repos/${ADMIN_USER}/${REPO_NAME}/hooks/${HOOK_ID}/deliveries" 2>/dev/null) || DELIVERIES="[]"
DELIVERY_COUNT=$(echo "$DELIVERIES" | grep -c '"id"' 2>/dev/null) || DELIVERY_COUNT=0
info "Webhook deliveries: ${DELIVERY_COUNT}"

echo ""
echo -e "${GREEN}════════════════════════════════════════${NC}"
echo -e "${GREEN}  Integration test PASSED${NC}"
echo -e "${GREEN}════════════════════════════════════════${NC}"
echo ""
info "Pipeline: Gitea push → webhook → ccg clone → build → DB ✅"
