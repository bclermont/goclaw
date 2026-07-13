-- Drop tool_calls and hook_executions columns from traces table

ALTER TABLE traces
  DROP COLUMN IF EXISTS tool_calls,
  DROP COLUMN IF EXISTS hook_executions;
