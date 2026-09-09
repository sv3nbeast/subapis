-- Keep explicit attachment intent separate from retrieved source snapshots.
-- NULL denotes legacy messages; existing link rows remain their only explicit
-- evidence. Never infer explicit intent from automatically retrieved sources.
ALTER TABLE web_chat_messages ADD COLUMN IF NOT EXISTS explicit_document_ids JSONB;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'web_chat_messages'::regclass
          AND conname = 'web_chat_explicit_documents_array'
    ) THEN
        ALTER TABLE web_chat_messages ADD CONSTRAINT web_chat_explicit_documents_array
            CHECK (explicit_document_ids IS NULL OR jsonb_typeof(explicit_document_ids)='array');
    END IF;
END $$;
