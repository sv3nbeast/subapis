-- Never backfill historical targets from the mutable session target.
ALTER TABLE web_chat_messages ADD COLUMN IF NOT EXISTS model varchar(200) NOT NULL DEFAULT '';
ALTER TABLE web_chat_messages ADD COLUMN IF NOT EXISTS platform varchar(50) NOT NULL DEFAULT '';
ALTER TABLE web_chat_messages ADD COLUMN IF NOT EXISTS group_id bigint;
