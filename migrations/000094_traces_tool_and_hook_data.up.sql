-- Add tool_calls and hook_executions JSONB columns to traces table for trace detail storage

ALTER TABLE traces
  ADD COLUMN IF NOT EXISTS tool_calls JSONB DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS hook_executions JSONB DEFAULT '[]'::jsonb;
