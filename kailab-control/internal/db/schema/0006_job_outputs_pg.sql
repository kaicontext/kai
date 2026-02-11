-- Add outputs and summary columns to jobs table
ALTER TABLE jobs ADD COLUMN outputs TEXT DEFAULT '{}';
ALTER TABLE jobs ADD COLUMN summary TEXT DEFAULT '';
