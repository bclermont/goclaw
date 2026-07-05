package tools

import (
	"context"
	"fmt"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// SkillContentTool returns the raw SKILL.md markdown content for a skill,
// looked up by name, slug, or UUID, sourced exclusively from the DB skill store.
type SkillContentTool struct {
	skills store.SkillManageStore
}

// NewSkillContentTool creates a skill_content tool backed by the DB skill store.
func NewSkillContentTool(skillStore store.SkillManageStore) *SkillContentTool {
	return &SkillContentTool{skills: skillStore}
}

// Name returns the tool's registered name.
func (t *SkillContentTool) Name() string { return "skill_content" }

// Description returns the tool description shown to the model.
func (t *SkillContentTool) Description() string {
	return "Fetch the raw SKILL.md markdown content of a skill by name, slug, or ID, directly from the database. " +
		"Use this to inspect a skill's full instructions before patching it or delegating work that depends on it."
}

// Parameters returns the JSON schema for tool arguments.
func (t *SkillContentTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill": map[string]any{
				"type":        "string",
				"description": "Skill name, slug, or UUID to fetch content for.",
			},
		},
		"required": []string{"skill"},
	}
}

// Execute looks up the skill's content in the DB store and returns it.
func (t *SkillContentTool) Execute(ctx context.Context, args map[string]any) *Result {
	name, _ := args["skill"].(string)
	if name == "" {
		return ErrorResult("skill is required")
	}

	info, ok := t.skills.GetSkill(ctx, name)
	if !ok {
		return ErrorResult(fmt.Sprintf("skill %q not found", name))
	}

	content, ok := t.skills.LoadSkill(ctx, info.Slug)
	if !ok {
		return ErrorResult(fmt.Sprintf("failed to load content for skill %q", name))
	}

	return NewResult(fmt.Sprintf("### Skill: %s (slug: %s, v%d)\n\n%s", info.Name, info.Slug, info.Version, content))
}
