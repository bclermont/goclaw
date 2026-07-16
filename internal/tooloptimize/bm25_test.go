package tooloptimize

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTokenizeBreaksDelimitedNames(t *testing.T) {
	assert.Equal(t, []string{"mcp", "git", "git", "clone"}, tokenize("mcp_git__git-clone"))
	assert.Empty(t, tokenize(""))
}

func TestBM25RanksTokenMatchOverNonMatch(t *testing.T) {
	texts := []string{
		"git clone clone a git repository url",
		"send an email message to recipient",
		"current weather for a city",
	}
	ix := buildBM25Index(texts)
	q := tokenize("git repository")
	assert.Greater(t, ix.score(q, 0), 0.0, "git tool should score")
	assert.Equal(t, 0.0, ix.score(q, 1), "email tool should not")
	assert.Equal(t, 0.0, ix.score(q, 2), "weather tool should not")
}

// domainSet via BM25 matches tools by broken-out name tokens that a raw
// substring match on the delimited name would miss.
func TestDomainSetBM25MatchesTokenizedName(t *testing.T) {
	tools := []ToolDesc{
		{Name: "terminal", Description: "shell", Core: true},
		{Name: "mcp_git__clone", Description: "clone a repo"},
		{Name: "mcp_gmail__send", Description: "send an email"},
	}
	set := Profile{DomainKeywords: []string{"git"}}.domainSet(tools)
	assert.True(t, set["terminal"], "core always in-domain")
	assert.True(t, set["mcp_git__clone"], "git matched via tokenized name")
	assert.False(t, set["mcp_gmail__send"], "off-domain excluded")
}

func TestDomainSetEmptyKeywordsIncludesAll(t *testing.T) {
	tools := []ToolDesc{{Name: "a"}, {Name: "b", Core: true}}
	set := Profile{}.domainSet(tools)
	assert.True(t, set["a"])
	assert.True(t, set["b"])
}
