CREATE INDEX IF NOT EXISTS idx_inbox_recipient_archived_created_id
ON inbox_item(workspace_id, recipient_type, recipient_id, archived, created_at DESC, id DESC);
