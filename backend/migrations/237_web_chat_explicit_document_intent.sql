-- Keep explicit attachment intent separate from retrieved source snapshots.
-- NULL denotes legacy messages; existing link rows remain their only explicit
-- evidence. Never infer explicit intent from automatically retrieved sources.
ALTER TABLE web_chat_messages ADD COLUMN IF NOT EXISTS explicit_document_ids JSONB;
ALTER TABLE web_chat_messages ADD CONSTRAINT web_chat_explicit_documents_array
CHECK (explicit_document_ids IS NULL OR jsonb_typeof(explicit_document_ids)='array');
