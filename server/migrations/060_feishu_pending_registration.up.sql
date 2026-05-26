CREATE TABLE feishu_pending_registration (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_token TEXT NOT NULL UNIQUE,
    open_id TEXT NOT NULL,
    union_id TEXT,
    tenant_key TEXT,
    name TEXT,
    avatar_url TEXT,
    raw_profile JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_feishu_pending_session_token ON feishu_pending_registration(session_token);
CREATE INDEX idx_feishu_pending_expires_at ON feishu_pending_registration(expires_at);
