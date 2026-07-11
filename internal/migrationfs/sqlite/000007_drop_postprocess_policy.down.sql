CREATE TABLE IF NOT EXISTS ccg_postprocess_policy_state (
    namespace text NOT NULL,
    tool text NOT NULL,
    policy text NOT NULL,
    updated_at datetime NOT NULL,
    PRIMARY KEY (namespace, tool)
);

CREATE TABLE IF NOT EXISTS ccg_postprocess_run_logs (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL,
    tool text NOT NULL,
    policy text NOT NULL,
    source text NOT NULL,
    status text NOT NULL,
    failed_steps text NOT NULL DEFAULT '[]',
    skipped_steps text NOT NULL DEFAULT '[]',
    created_at datetime NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pp_log_ns_tool_time
ON ccg_postprocess_run_logs(namespace, tool, created_at DESC);
