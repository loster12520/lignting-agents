package fixtures

import (
	"context"
	"database/sql"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// seedAudit writes the fixture audit entries through store.AppendAudit so the
// production hash chain (canonical detail + prev_hash) stays verifiable. Audit
// rows must not be inserted by raw SQL: fixtures would otherwise poison
// VerifyAuditChain for every AC.
func seedAudit(ctx context.Context, db *sql.DB, r *SeedResult) error {
	st := &store.Store{DB: db}
	entries := []domain.AuditEntry{
		{ActorType: "user", ActorID: OwnerID, Action: "setup", ObjectType: "owner", ObjectID: OwnerID, Result: "ok",
			Detail: MustJSON(map[string]any{"mode": "acceptance"})},
		{ActorType: "user", ActorID: OwnerID, Action: "approve", ObjectType: "approval", ObjectID: ApprovalG1ID, Result: "ok",
			Detail: MustJSON(map[string]any{"decision": "approved"})},
		{ActorType: "agent", ActorID: AgentDevLeadID, Action: "task.transition", ObjectType: "task", ObjectID: TaskID, Result: "ok",
			Detail: MustJSON(map[string]any{"from": "in_review", "to": "approved"})},
		{ActorType: "agent", ActorID: AgentDevAID, Action: "tool.write_file", ObjectType: "artifact", ObjectID: ArtifactCodeID, Result: "ok",
			Detail: MustJSON(map[string]any{"path": "internal/core/export.go"})},
	}
	for _, e := range entries {
		out, err := st.AppendAudit(ctx, e)
		if err != nil {
			return err
		}
		r.AuditSeqs = append(r.AuditSeqs, out.Seq)
	}
	return nil
}
