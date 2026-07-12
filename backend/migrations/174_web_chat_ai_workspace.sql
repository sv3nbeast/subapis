-- Web Chat AI workspace: projects, prompt templates and immutable linear history.

CREATE TABLE IF NOT EXISTS web_chat_templates (
    id bigserial PRIMARY KEY,
    scope varchar(20) NOT NULL CHECK (scope IN ('system', 'personal')),
    user_id bigint REFERENCES users(id) ON DELETE CASCADE,
    source_template_id bigint REFERENCES web_chat_templates(id) ON DELETE SET NULL,
    name varchar(120) NOT NULL,
    category varchar(80) NOT NULL DEFAULT '',
    description varchar(500) NOT NULL DEFAULT '',
    body text NOT NULL,
    variables jsonb NOT NULL DEFAULT '[]'::jsonb,
    language varchar(16) NOT NULL DEFAULT 'zh-CN',
    enabled boolean NOT NULL DEFAULT true,
    sort_order integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT chk_web_chat_template_owner CHECK (
        (scope = 'system' AND user_id IS NULL) OR (scope = 'personal' AND user_id IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_web_chat_templates_user_scope_sort
    ON web_chat_templates(user_id, scope, enabled DESC, sort_order, id)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS web_chat_projects (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name varchar(120) NOT NULL,
    description varchar(500) NOT NULL DEFAULT '',
    color varchar(16) NOT NULL DEFAULT '#14b8a6',
    sort_order integer NOT NULL DEFAULT 0,
    default_group_id bigint REFERENCES groups(id) ON DELETE SET NULL,
    default_model varchar(255),
    default_template_id bigint REFERENCES web_chat_templates(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS idx_web_chat_projects_user_sort
    ON web_chat_projects(user_id, sort_order, updated_at DESC, id)
    WHERE deleted_at IS NULL;

ALTER TABLE web_chat_sessions
    ADD COLUMN IF NOT EXISTS project_id bigint REFERENCES web_chat_projects(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS active_leaf_message_id bigint,
    ADD COLUMN IF NOT EXISTS default_template_id bigint REFERENCES web_chat_templates(id) ON DELETE SET NULL;

ALTER TABLE web_chat_messages
    ADD COLUMN IF NOT EXISTS logical_id bigint,
    ADD COLUMN IF NOT EXISTS parent_message_id bigint REFERENCES web_chat_messages(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS version_index integer NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS version_reason varchar(32) NOT NULL DEFAULT 'original',
    ADD COLUMN IF NOT EXISTS template_id bigint REFERENCES web_chat_templates(id) ON DELETE SET NULL;

-- Existing visible messages become one chronological active path. Previously soft-deleted
-- rows deliberately stay hidden and are not reconstructed.
UPDATE web_chat_messages SET logical_id = id WHERE logical_id IS NULL;

WITH ordered AS (
    SELECT id, lag(id) OVER (PARTITION BY session_id ORDER BY created_at, id) AS parent_id
    FROM web_chat_messages
    WHERE deleted_at IS NULL
)
UPDATE web_chat_messages m
SET parent_message_id = ordered.parent_id
FROM ordered
WHERE m.id = ordered.id AND m.parent_message_id IS NULL;

WITH leaves AS (
    SELECT DISTINCT ON (session_id) session_id, id
    FROM web_chat_messages
    WHERE deleted_at IS NULL
    ORDER BY session_id, created_at DESC, id DESC
)
UPDATE web_chat_sessions s
SET active_leaf_message_id = leaves.id
FROM leaves
WHERE s.id = leaves.session_id AND s.active_leaf_message_id IS NULL;

ALTER TABLE web_chat_messages ALTER COLUMN logical_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_web_chat_messages_session_logical_version
    ON web_chat_messages(session_id, logical_id, version_index, id)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_web_chat_messages_parent
    ON web_chat_messages(parent_message_id)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_web_chat_sessions_user_project
    ON web_chat_sessions(user_id, project_id, updated_at DESC)
    WHERE deleted_at IS NULL;

-- Seed safe, editable system templates once. Variables are plain text placeholders.
INSERT INTO web_chat_templates(scope, name, category, description, body, variables, language, sort_order)
SELECT seed.scope, seed.name, seed.category, seed.description, seed.body, seed.variables::jsonb, seed.language, seed.sort_order
FROM (VALUES
 ('system','总结提炼','总结','提炼核心观点与结论','请总结以下内容，输出核心结论、关键要点和待办事项：\n\n{{content}}','[{"name":"content","label":"待总结内容","required":true,"default_value":"","type":"multiline"}]','zh-CN',10),
 ('system','专业改写','写作','调整语气并提升表达质量','请将以下内容改写为{{tone}}风格，保持事实和原意不变：\n\n{{content}}','[{"name":"tone","label":"目标语气","required":true,"default_value":"专业、简洁","type":"singleline"},{"name":"content","label":"原文","required":true,"default_value":"","type":"multiline"}]','zh-CN',20),
 ('system','会议纪要','会议','生成结构化会议纪要','请根据以下会议记录生成纪要，包含议题、结论、行动项、负责人和截止时间：\n\n{{notes}}','[{"name":"notes","label":"会议记录","required":true,"default_value":"","type":"multiline"}]','zh-CN',30),
 ('system','方案规划','规划','将目标拆解为可执行方案','请为以下目标制定方案，包含背景、目标、里程碑、资源、风险和验收标准：\n\n{{goal}}','[{"name":"goal","label":"目标","required":true,"default_value":"","type":"multiline"}]','zh-CN',40),
 ('system','优缺点分析','分析','比较选项并给出建议','请分析{{subject}}的优点、缺点、适用场景、风险，并给出建议。','[{"name":"subject","label":"分析对象","required":true,"default_value":"","type":"multiline"}]','zh-CN',50),
 ('system','行动清单','效率','把内容转成可执行清单','请将以下内容转为按优先级排序的行动清单，标注负责人、截止时间和完成标准：\n\n{{content}}','[{"name":"content","label":"原始内容","required":true,"default_value":"","type":"multiline"}]','zh-CN',60),
 ('system','Executive Summary','Summary','Create a concise executive summary','Summarize the following into decisions, key points, risks, and action items:\n\n{{content}}','[{"name":"content","label":"Content","required":true,"default_value":"","type":"multiline"}]','en',110),
 ('system','Action Plan','Planning','Turn a goal into an actionable plan','Create an actionable plan for this goal with milestones, owners, risks, and acceptance criteria:\n\n{{goal}}','[{"name":"goal","label":"Goal","required":true,"default_value":"","type":"multiline"}]','en',120)
) AS seed(scope,name,category,description,body,variables,language,sort_order)
WHERE NOT EXISTS (SELECT 1 FROM web_chat_templates WHERE scope='system');
