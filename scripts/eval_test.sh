#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(cd "${SCRIPT_DIR}/.." && pwd)

source "${ROOT_DIR}/scripts/eval.sh"

assert_equals() {
    local expected="$1" actual="$2"
    [ "$expected" = "$actual" ] || {
        echo "expected '$expected', got '$actual'" >&2
        exit 1
    }
}

assert_contains() {
    local haystack="$1" needle="$2"
    [[ "$haystack" == *"$needle"* ]] || {
        echo "expected '$haystack' to contain '$needle'" >&2
        exit 1
    }
}

assert_fails() {
    local description="$1"; shift
    if ( "$@" ) >/dev/null 2>&1; then
        echo "expected failure: ${description}" >&2
        exit 1
    fi
}

reset_test_env() {
    CCG_BIN="ccg"
    CCG_EVAL_CORPUS="testdata/eval"
    CCG_EVAL_NAMESPACE="eval"
    CCG_EVAL_DB_DRIVER="sqlite"
    CCG_EVAL_DB_DSN=""
    CCG_EVAL_KEEP_DB=0
    TEST_TMPDIR=""
}

test_eval_script_exists_and_is_executable() {
    [ -x "${ROOT_DIR}/scripts/eval.sh" ] || {
        echo "expected scripts/eval.sh to exist and be executable" >&2
        exit 1
    }
}

test_eval_default_db_path_uses_temp_dir() {
    local path
    path=$(eval_default_db_path)
    assert_contains "$path" "/eval.db"
    [ -d "$(dirname "$path")" ] || {
        echo "expected temp dir to exist for path: $path" >&2
        exit 1
    }
    rm -rf "$(dirname "$path")"
}

test_eval_build_and_run_cmds_use_shared_db_namespace_and_corpus() {
    local db migrate_cmd build_cmd run_cmd
    db="/tmp/eval-script-test.db"
    migrate_cmd=$(eval_migrate_cmd "$db")
    build_cmd=$(eval_build_cmd "$db")
    run_cmd=$(eval_run_cmd "$db")

    assert_equals "ccg migrate --db-driver sqlite --db-dsn /tmp/eval-script-test.db" "$migrate_cmd"
    assert_equals "ccg build testdata/eval --db-driver sqlite --db-dsn /tmp/eval-script-test.db --namespace eval" "$build_cmd"
    assert_equals "ccg eval --corpus testdata/eval --suite all --db-driver sqlite --db-dsn /tmp/eval-script-test.db --namespace eval" "$run_cmd"
}

test_eval_main_invokes_build_then_eval_in_order() {
    local tmpdir stub_dir log db
    tmpdir=$(mktemp -d)
    stub_dir="${tmpdir}/bin"
    log="${tmpdir}/ccg.log"
    db="${tmpdir}/shared.db"
    mkdir -p "$stub_dir"

    cat > "${stub_dir}/ccg" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "\$*" >> "$log"
EOF
    chmod +x "${stub_dir}/ccg"

    PATH="${stub_dir}:$PATH" CCG_EVAL_DB_DSN="$db" main >/dev/null

    [ -f "$log" ] || {
        echo "expected stub log to be created" >&2
        exit 1
    }

    local line1 line2 line3
    line1=$(sed -n '1p' "$log")
    line2=$(sed -n '2p' "$log")
    line3=$(sed -n '3p' "$log")
    assert_equals "migrate --db-driver sqlite --db-dsn $db" "$line1"
    assert_equals "build testdata/eval --db-driver sqlite --db-dsn $db --namespace eval" "$line2"
    assert_equals "eval --corpus testdata/eval --suite all --db-driver sqlite --db-dsn $db --namespace eval" "$line3"

    rm -rf "$tmpdir"
}

test_eval_main_supports_multitoken_ccg_bin_override() {
    local tmpdir stub_dir log db
    tmpdir=$(mktemp -d)
    stub_dir="${tmpdir}/bin"
    log="${tmpdir}/ccg.log"
    db="${tmpdir}/shared.db"
    mkdir -p "$stub_dir"

    cat > "${stub_dir}/ccg-stub" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "\$*" >> "$log"
EOF
    chmod +x "${stub_dir}/ccg-stub"

    PATH="${stub_dir}:$PATH" CCG_BIN='bash ccg-stub' CCG_EVAL_DB_DSN="$db" main >/dev/null

    local line1 line2 line3
    line1=$(sed -n '1p' "$log")
    line2=$(sed -n '2p' "$log")
    line3=$(sed -n '3p' "$log")
    assert_equals "migrate --db-driver sqlite --db-dsn $db" "$line1"
    assert_equals "build testdata/eval --db-driver sqlite --db-dsn $db --namespace eval" "$line2"
    assert_equals "eval --corpus testdata/eval --suite all --db-driver sqlite --db-dsn $db --namespace eval" "$line3"

    rm -rf "$tmpdir"
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

run_test test_eval_script_exists_and_is_executable
run_test test_eval_default_db_path_uses_temp_dir
run_test test_eval_build_and_run_cmds_use_shared_db_namespace_and_corpus
run_test test_eval_main_invokes_build_then_eval_in_order
run_test test_eval_main_supports_multitoken_ccg_bin_override

echo "eval helper tests passed"
