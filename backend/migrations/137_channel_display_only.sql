-- Add display-only channels for user-visible pricing/catalog data.
-- Display-only channels can attach to groups that already belong to a real channel,
-- but must not participate in routing, model restriction, or real billing.

ALTER TABLE channels
    ADD COLUMN IF NOT EXISTS display_only BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN channels.display_only IS '展示渠道：仅用于用户侧渠道/价格展示，不参与调度、模型限制或真实计费';

DROP INDEX IF EXISTS idx_channel_groups_group_id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_groups_channel_group
    ON channel_groups(channel_id, group_id);

CREATE OR REPLACE FUNCTION enforce_single_effective_channel_group()
RETURNS TRIGGER AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM channels c
        WHERE c.id = NEW.channel_id
          AND c.display_only = FALSE
    ) THEN
        IF EXISTS (
            SELECT 1
            FROM channel_groups cg
            JOIN channels c ON c.id = cg.channel_id
            WHERE cg.group_id = NEW.group_id
              AND cg.channel_id <> NEW.channel_id
              AND c.display_only = FALSE
        ) THEN
            RAISE EXCEPTION 'group % already belongs to another effective channel', NEW.group_id
                USING ERRCODE = '23505';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_enforce_single_effective_channel_group ON channel_groups;
CREATE TRIGGER trg_enforce_single_effective_channel_group
    BEFORE INSERT OR UPDATE OF channel_id, group_id ON channel_groups
    FOR EACH ROW
    EXECUTE FUNCTION enforce_single_effective_channel_group();

CREATE OR REPLACE FUNCTION enforce_channel_display_only_transition()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.display_only = TRUE AND NEW.display_only = FALSE THEN
        IF EXISTS (
            SELECT 1
            FROM channel_groups own
            JOIN channel_groups other ON other.group_id = own.group_id
            JOIN channels c ON c.id = other.channel_id
            WHERE own.channel_id = NEW.id
              AND other.channel_id <> NEW.id
              AND c.display_only = FALSE
        ) THEN
            RAISE EXCEPTION 'display-only channel % cannot become effective while its groups belong to another effective channel', NEW.id
                USING ERRCODE = '23505';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_enforce_channel_display_only_transition ON channels;
CREATE TRIGGER trg_enforce_channel_display_only_transition
    BEFORE UPDATE OF display_only ON channels
    FOR EACH ROW
    EXECUTE FUNCTION enforce_channel_display_only_transition();

COMMENT ON TABLE channel_groups IS '渠道-分组关联表：真实渠道每个分组最多属于一个；展示渠道可复用分组';
