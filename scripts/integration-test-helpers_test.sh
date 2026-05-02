#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(cd "${SCRIPT_DIR}/.." && pwd)

source "${ROOT_DIR}/scripts/integration-test.sh"

assert_file_exists() {
    local path="$1"
    [ -f "$path" ] || {
        echo "expected file to exist: $path" >&2
        exit 1
    }
}

assert_equals() {
    local expected="$1" actual="$2"
    [ "$expected" = "$actual" ] || {
        echo "expected '$expected', got '$actual'" >&2
        exit 1
    }
}

reset_test_env() {
    ARTIFACT_DIR=""
    KEEP_CONTAINERS=0
    DUMP_ON_SUCCESS=0
    WEBHOOK_WAIT_SECONDS=60
    CCG_E2E_ALLOW_MCP_LOG_FALLBACK=0
    TMPDIR_CLONE=""
    TMPDIR_CLONE2=""
    COMPOSE_CMD="docker compose"
    COMPOSE_FILE="docker-compose.integration.yml"
    MCP_SESSION=""
    REPO_NAME="sample-go"
}

assert_fails() {
    local description="$1"; shift
    if ( "$@" ) >/dev/null 2>&1; then
        echo "expected failure: ${description}" >&2
        exit 1
    fi
}

test_artifact_dir_and_dump_path() {
    local tmp
    tmp=$(mktemp -d)
    ARTIFACT_DIR="${tmp}/artifacts"

    prepare_artifact_dir
    dump_text_artifact "ccg.log" "hello logs"

    assert_file_exists "${ARTIFACT_DIR}/ccg.log"
    grep -q "hello logs" "${ARTIFACT_DIR}/ccg.log"
    rm -rf "$tmp"
}

test_keep_containers_skips_compose_down() {
    local tmp marker
    tmp=$(mktemp -d)
    marker="${tmp}/compose-down-called"
    KEEP_CONTAINERS=1
    TMPDIR_CLONE=""
    TMPDIR_CLONE2=""
    COMPOSE_CMD="touch ${marker}"

    cleanup

    [ ! -e "$marker" ] || {
        echo "expected KEEP_CONTAINERS=1 to skip compose down" >&2
        exit 1
    }
    rm -rf "$tmp"
}

test_compose_does_not_eval_command_string() {
    local tmp marker
    tmp=$(mktemp -d)
    marker="${tmp}/eval-injection"
    COMPOSE_CMD="false ; touch ${marker}"

    assert_fails "compose should not eval shell metacharacters" compose ps
    [ ! -e "$marker" ] || {
        echo "compose evaluated shell metacharacters" >&2
        exit 1
    }
    rm -rf "$tmp"
}

test_mcp_text_extracts_content() {
    local resp text
    resp='{"result":{"content":[{"type":"text","text":"{\"total_nodes\":3}"}]}}'
    text=$(mcp_text "$resp")

    assert_equals '{"total_nodes":3}' "$text"
}

test_assert_mcp_ok_rejects_malformed_json() {
    assert_fails "malformed MCP JSON" assert_mcp_ok "bad_tool" '{not-json'
}

test_assert_mcp_ok_rejects_jsonrpc_error() {
    local resp
    resp='{"error":{"code":-32603,"message":"boom"}}'
    assert_fails "JSON-RPC error response" assert_mcp_ok "bad_tool" "$resp"
}

test_assert_mcp_ok_rejects_result_is_error() {
    local resp
    resp='{"result":{"isError":true,"content":[{"type":"text","text":"boom"}]}}'
    assert_fails "MCP result.isError response" assert_mcp_ok "bad_tool" "$resp"
}

test_assert_mcp_contains_rejects_missing_content_text() {
    local resp
    resp='{"result":{"content":[]}}'
    assert_fails "missing MCP content text" assert_mcp_contains "bad_tool" "$resp" "needle"
}

test_mcp_init_failure_requires_explicit_debug_override() {
    assert_fails "MCP init failure should be fatal by default" mcp_session_required ""
    CCG_E2E_ALLOW_MCP_LOG_FALLBACK=1
    mcp_session_required ""
}

test_extract_mcp_session_id_reads_header_case_insensitively() {
    local session
    session=$(extract_mcp_session_id $'HTTP/1.1 200 OK\r\nMcp-Session-Id: abc-123\r\n\r\n{}')
    assert_equals "abc-123" "$session"
}

test_log_fallback_detects_webhook_completion() {
    local logs
    logs='time=... webhook sync completed workspace=sample-go nodes=4'
    webhook_logs_completed "$logs" "sample-go"
}

test_log_fallback_matches_without_pipefail_sigpipe_risk() {
    local logs
    logs=$(printf 'noise %.0s' {1..2000})
    logs+=$'\ntime=... webhook sync completed workspace=sample-go nodes=4\n'
    webhook_logs_completed "$logs" "sample-go"
    ! webhook_logs_completed "$logs" "sample-calc"
    assert_equals "4" "$(extract_last_webhook_node_count "$logs")"
}

test_log_fallback_detects_webhook_failure() {
    local logs
    logs='time=... webhook build failed workspace=sample-go error=boom'
    webhook_logs_failed "$logs" "sample-go"
}

test_helper_env_isolation_mutates_globals() {
    KEEP_CONTAINERS=1
    COMPOSE_CMD="false"
}

test_helper_env_isolation_observes_defaults() {
    assert_equals "0" "$KEEP_CONTAINERS"
    assert_equals "docker compose" "$COMPOSE_CMD"
}

run_test() {
    local name="$1"
    if ( reset_test_env; "$name" ); then
        printf 'ok - %s\n' "$name"
    else
        printf 'not ok - %s\n' "$name" >&2
        exit 1
    fi
}

run_test test_artifact_dir_and_dump_path
run_test test_keep_containers_skips_compose_down
run_test test_compose_does_not_eval_command_string
run_test test_mcp_text_extracts_content
run_test test_assert_mcp_ok_rejects_malformed_json
run_test test_assert_mcp_ok_rejects_jsonrpc_error
run_test test_assert_mcp_ok_rejects_result_is_error
run_test test_assert_mcp_contains_rejects_missing_content_text
run_test test_mcp_init_failure_requires_explicit_debug_override
run_test test_extract_mcp_session_id_reads_header_case_insensitively
run_test test_log_fallback_detects_webhook_completion
run_test test_log_fallback_matches_without_pipefail_sigpipe_risk
run_test test_log_fallback_detects_webhook_failure
run_test test_helper_env_isolation_mutates_globals
run_test test_helper_env_isolation_observes_defaults

echo "integration helper tests passed"
