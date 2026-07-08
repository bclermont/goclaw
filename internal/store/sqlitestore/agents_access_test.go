//go:build sqlite || sqliteonly

package sqlitestore

import (
	"testing"

	"github.com/google/uuid"
)

// Regression test for agents.is_public: unlike is_default (a tenant-wide
// singleton), any number of agents can independently grant access to any
// user without an explicit agent_shares row, once is_public is true.
func TestSQLiteAgentStore_CanAccess_IsPublic(t *testing.T) {
	db, tenantID, agentID := newAgentUpdateTestFixture(t)
	store := NewSQLiteAgentStore(db)
	ctx := sqliteTenantCtx(tenantID)

	strangerUserID := "anonymous-visitor-1"

	ok, _, err := store.CanAccess(ctx, agentID, strangerUserID)
	if err != nil {
		t.Fatalf("CanAccess before is_public: %v", err)
	}
	if ok {
		t.Fatalf("expected no access before is_public is set")
	}

	if err := store.Update(ctx, agentID, map[string]any{"is_public": true}); err != nil {
		t.Fatalf("Update is_public: %v", err)
	}

	ok, role, err := store.CanAccess(ctx, agentID, strangerUserID)
	if err != nil {
		t.Fatalf("CanAccess after is_public: %v", err)
	}
	if !ok {
		t.Fatalf("expected access after is_public is set")
	}
	if role != "user" {
		t.Errorf("role: got %q, want %q", role, "user")
	}

	// A different, unrelated agent must remain private — is_public is not a
	// singleton and must not leak access to sibling agents.
	otherAgentID := uuid.Must(uuid.NewV7())
	if _, err := db.Exec(
		`INSERT INTO agents (id, tenant_id, agent_key, agent_type, status, provider, model, owner_id)
		 VALUES (?,?,?,'predefined','active','test','test-model','owner')`,
		otherAgentID.String(), tenantID.String(), "au-sibling-"+otherAgentID.String()[:8]); err != nil {
		t.Fatalf("seed sibling agent: %v", err)
	}
	ok, _, err = store.CanAccess(ctx, otherAgentID, strangerUserID)
	if err != nil {
		t.Fatalf("CanAccess sibling agent: %v", err)
	}
	if ok {
		t.Fatalf("expected sibling agent to remain private")
	}
}
