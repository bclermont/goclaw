-- Add raw markdown content column to skills so skill content can live only
-- in the database at runtime (no disk reads for SKILL.md text).
ALTER TABLE skills ADD COLUMN content TEXT;
