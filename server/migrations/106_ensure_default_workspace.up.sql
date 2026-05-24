-- Ensure the shared default workspace exists and is never lost on redeployment.
-- Idempotent: ON CONFLICT DO NOTHING means re-running is safe.
INSERT INTO workspace (name, slug, description, issue_prefix)
VALUES ('Mindverse All', 'mindverse-all', 'Default shared workspace for all users', 'MAL')
ON CONFLICT (slug) DO NOTHING;

-- Ensure 叶锐坚 (1007918011@qq.com) is an admin of mindverse-all.
-- Uses ON CONFLICT to upsert: if already a member, promote to admin; if not, insert.
INSERT INTO member (workspace_id, user_id, role)
SELECT w.id, u.id, 'admin'
FROM workspace w
CROSS JOIN "user" u
WHERE w.slug = 'mindverse-all'
  AND u.email = '1007918011@qq.com'
ON CONFLICT (workspace_id, user_id)
DO UPDATE SET role = 'admin';

-- Backfill any remaining users who are not yet members.
INSERT INTO member (workspace_id, user_id, role)
SELECT w.id, u.id, 'member'
FROM workspace w
CROSS JOIN "user" u
WHERE w.slug = 'mindverse-all'
ON CONFLICT (workspace_id, user_id) DO NOTHING;
