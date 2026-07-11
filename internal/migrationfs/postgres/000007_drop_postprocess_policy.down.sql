CREATE TABLE IF NOT EXISTS ccg_postprocess_policy_state (
    namespace varchar(256) NOT NULL,
    tool varchar(64) NOT NULL,
    policy varchar(32) NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (namespace, tool)
);

CREATE TABLE IF NOT EXISTS ccg_postprocess_run_logs (
    id bigserial PRIMARY KEY,
    namespace varchar(256) NOT NULL,
    tool varchar(64) NOT NULL,
    policy varchar(32) NOT NULL,
    source varchar(16) NOT NULL,
    status varchar(16) NOT NULL,
    failed_steps jsonb NOT NULL DEFAULT '[]'::jsonb,
    skipped_steps jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pp_log_ns_tool_time
ON ccg_postprocess_run_logs(namespace, tool, created_at DESC);
