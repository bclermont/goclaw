-- Per-agent tool-list optimization level (0..5). Drives how the agent's tool
-- catalog is shaped before it reaches the model: 0 = off (all inline), through
-- scope/defer-tail, up to 5 = aggressive (core inline, rest behind search).
-- See internal/tooloptimize for the ladder semantics.
ALTER TABLE agents
    ADD COLUMN tool_optimization_level INT NOT NULL DEFAULT 0;

ALTER TABLE agents
    ADD CONSTRAINT chk_agents_tool_optimization_level
        CHECK (tool_optimization_level BETWEEN 0 AND 5);
