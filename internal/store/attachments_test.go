package store_test

import (
	"context"
	"testing"

	"github.com/lignting/lignting-agents/internal/store"
)

func TestAttachmentsStoreAndMigrateV2(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	var version int
	if err := st.DB.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("migration version: %v", err)
	}
	if version != store.SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, store.SchemaVersion)
	}
	var table string
	if err := st.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='attachments'").Scan(&table); err != nil {
		t.Fatalf("attachments table: %v", err)
	}
	a, err := st.CreateAttachment(ctx, store.Attachment{ID: "att_1", ConversationID: "conv_1", Filename: "a.txt", Mime: "text/plain", Size: 3})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := st.GetAttachment(ctx, a.ID)
	if err != nil || got.Filename != "a.txt" || got.CreatedAt == "" {
		t.Fatalf("get = %+v err=%v", got, err)
	}
	list, err := st.ListAttachmentsByConversation(ctx, "conv_1", false, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d err=%v", len(list), err)
	}
	if _, err := st.SoftDeleteAttachment(ctx, a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ = st.ListAttachmentsByConversation(ctx, "conv_1", false, 10)
	if len(list) != 0 {
		t.Fatalf("deleted attachment still listed: %d", len(list))
	}
	if list, _ := st.ListAttachmentsByConversation(ctx, "conv_1", true, 10); len(list) != 1 {
		t.Fatalf("include_deleted list = %d, want 1", len(list))
	}
}
