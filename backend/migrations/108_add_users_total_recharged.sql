-- Add missing total_recharged field expected by current user schema.
ALTER TABLE users ADD COLUMN IF NOT EXISTS total_recharged DECIMAL(20,8);

UPDATE users
SET total_recharged = 0
WHERE total_recharged IS NULL;

ALTER TABLE users
ALTER COLUMN total_recharged SET DEFAULT 0;

ALTER TABLE users
ALTER COLUMN total_recharged SET NOT NULL;
