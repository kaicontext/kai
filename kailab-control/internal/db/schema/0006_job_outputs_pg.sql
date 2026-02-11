-- Add outputs and summary columns to jobs table
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS outputs TEXT DEFAULT '{}';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS summary TEXT DEFAULT '';
