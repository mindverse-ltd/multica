-- Backfill access to the shared mindverse-all workspace for users who
-- registered before the automatic membership path existed.
--
-- This is intentionally idempotent: if the workspace does not exist yet the
-- SELECT returns no rows, and existing members are skipped by the unique
-- (workspace_id, user_id) constraint.
INSERT INTO member (workspace_id, user_id, role)
SELECT w.id, u.id, 'member'
FROM workspace w
CROSS JOIN "user" u
WHERE w.slug = 'mindverse-all'
ON CONFLICT (workspace_id, user_id) DO NOTHING;
