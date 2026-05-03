UPDATE nodes SET
    qualified_name = COALESCE(qualified_name, ''),
    kind = COALESCE(kind, ''),
    name = COALESCE(name, ''),
    file_path = COALESCE(file_path, ''),
    start_line = COALESCE(start_line, 0),
    end_line = COALESCE(end_line, 0);

UPDATE edges SET
    kind = COALESCE(kind, ''),
    fingerprint = COALESCE(fingerprint, '');

UPDATE communities SET
    key = COALESCE(key, '');

UPDATE community_memberships SET
    community_id = COALESCE(community_id, 0),
    node_id = COALESCE(node_id, 0);

UPDATE flows SET
    name = COALESCE(name, '');

UPDATE flow_memberships SET
    flow_id = COALESCE(flow_id, 0),
    node_id = COALESCE(node_id, 0);

ALTER TABLE nodes
    ALTER COLUMN qualified_name SET NOT NULL,
    ALTER COLUMN kind SET NOT NULL,
    ALTER COLUMN name SET NOT NULL,
    ALTER COLUMN file_path SET NOT NULL,
    ALTER COLUMN start_line SET NOT NULL,
    ALTER COLUMN end_line SET NOT NULL;

ALTER TABLE edges
    ALTER COLUMN kind SET NOT NULL,
    ALTER COLUMN fingerprint SET NOT NULL;

ALTER TABLE communities
    ALTER COLUMN key SET NOT NULL;

ALTER TABLE community_memberships
    ALTER COLUMN community_id SET NOT NULL,
    ALTER COLUMN node_id SET NOT NULL;

ALTER TABLE flows
    ALTER COLUMN name SET NOT NULL;

ALTER TABLE flow_memberships
    ALTER COLUMN flow_id SET NOT NULL,
    ALTER COLUMN node_id SET NOT NULL;
