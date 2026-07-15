ALTER TABLE agents DROP CONSTRAINT IF EXISTS chk_agents_tool_optimization_level;
ALTER TABLE agents DROP COLUMN IF EXISTS tool_optimization_level;
