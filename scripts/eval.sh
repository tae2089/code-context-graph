#!/usr/bin/env bash
set -euo pipefail

CCG_BIN=${CCG_BIN:-ccg}
CCG_EVAL_CORPUS=${CCG_EVAL_CORPUS:-testdata/eval}
CCG_EVAL_NAMESPACE=${CCG_EVAL_NAMESPACE:-eval}
CCG_EVAL_DB_DRIVER=${CCG_EVAL_DB_DRIVER:-sqlite}
CCG_EVAL_DB_DSN=${CCG_EVAL_DB_DSN:-}
CCG_EVAL_KEEP_DB=${CCG_EVAL_KEEP_DB:-0}

eval_default_db_path() {
    local tmpdir
    tmpdir=$(mktemp -d)
    printf '%s/eval.db\n' "$tmpdir"
}

eval_db_dsn() {
    if [ -n "$CCG_EVAL_DB_DSN" ]; then
        printf '%s\n' "$CCG_EVAL_DB_DSN"
        return 0
    fi
    eval_default_db_path
}

eval_build_cmd() {
    local db="$1"
    printf '%s\n' "$CCG_BIN build $CCG_EVAL_CORPUS --db-driver $CCG_EVAL_DB_DRIVER --db-dsn $db --namespace $CCG_EVAL_NAMESPACE"
}

eval_migrate_cmd() {
    local db="$1"
    printf '%s\n' "$CCG_BIN migrate --db-driver $CCG_EVAL_DB_DRIVER --db-dsn $db"
}

eval_run_cmd() {
    local db="$1"
    printf '%s\n' "$CCG_BIN eval --corpus $CCG_EVAL_CORPUS --suite all --db-driver $CCG_EVAL_DB_DRIVER --db-dsn $db --namespace $CCG_EVAL_NAMESPACE"
}

run_eval_build() {
    local db="$1"
    # shellcheck disable=SC2086
    $CCG_BIN build "$CCG_EVAL_CORPUS" \
        --db-driver "$CCG_EVAL_DB_DRIVER" \
        --db-dsn "$db" \
        --namespace "$CCG_EVAL_NAMESPACE"
}

run_eval_migrate() {
    local db="$1"
    # shellcheck disable=SC2086
    $CCG_BIN migrate \
        --db-driver "$CCG_EVAL_DB_DRIVER" \
        --db-dsn "$db"
}

run_eval() {
    local db="$1"
    # shellcheck disable=SC2086
    $CCG_BIN eval \
        --corpus "$CCG_EVAL_CORPUS" \
        --suite all \
        --db-driver "$CCG_EVAL_DB_DRIVER" \
        --db-dsn "$db" \
        --namespace "$CCG_EVAL_NAMESPACE"
}

cleanup_eval_db() {
    local db="$1"
    if [ "$CCG_EVAL_KEEP_DB" = "1" ]; then
        return 0
    fi
    rm -f "$db"
    rmdir "$(dirname "$db")" 2>/dev/null || true
}

main() {
    local db
    db=$(eval_db_dsn)
    trap "cleanup_eval_db '$db'" EXIT
    run_eval_migrate "$db"
    run_eval_build "$db"
    run_eval "$db"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
