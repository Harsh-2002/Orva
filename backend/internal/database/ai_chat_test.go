package database

import "testing"

func TestDeleteAllConversationsCascadesHistory(t *testing.T) {
	db := newTestDB(t)

	first := &AIConversation{Title: "First"}
	second := &AIConversation{Title: "Second"}
	for _, conversation := range []*AIConversation{first, second} {
		if err := db.CreateConversation(conversation); err != nil {
			t.Fatalf("CreateConversation: %v", err)
		}
	}
	message := &AIMessage{ConversationID: first.ID, Role: "assistant", Content: "done"}
	if err := db.InsertMessage(message); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if err := db.InsertToolCall(&AIToolCall{
		ConversationID: first.ID,
		MessageID:      message.ID,
		ToolName:       "list_functions",
		Status:         "succeeded",
	}); err != nil {
		t.Fatalf("InsertToolCall: %v", err)
	}

	deleted, err := db.DeleteAllConversations()
	if err != nil {
		t.Fatalf("DeleteAllConversations: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	for _, table := range []string{"ai_conversations", "ai_messages", "ai_tool_calls"} {
		var count int
		if err := db.read.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Errorf("%s has %d rows after clear, want 0", table, count)
		}
	}

	deleted, err = db.DeleteAllConversations()
	if err != nil || deleted != 0 {
		t.Errorf("second clear: deleted=%d err=%v, want 0, nil", deleted, err)
	}
}
