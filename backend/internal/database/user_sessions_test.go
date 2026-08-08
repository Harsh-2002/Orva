package database

import (
	"testing"
	"time"
)

// TestDeleteUserSessionsExcept guards the M6 fix: changing a password must be
// able to revoke every OTHER session for the user while keeping the current one
// so a stolen session cookie stops working immediately.
func TestDeleteUserSessionsExcept(t *testing.T) {
	db := newTestDB(t)

	user, err := db.CreateUser("operator", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}

	var tokens []string
	for i := 0; i < 3; i++ {
		s, err := db.CreateSession(user.ID, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, s.Token)
	}
	keep := tokens[0]

	revoked, err := db.DeleteUserSessionsExcept(user.ID, keep)
	if err != nil {
		t.Fatal(err)
	}
	if revoked != 2 {
		t.Errorf("revoked = %d, want 2", revoked)
	}

	remaining, err := db.ListSessionsForUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Fatalf("remaining sessions = %d, want 1", len(remaining))
	}
	if remaining[0].Token != keep {
		t.Errorf("kept token = %q, want %q", remaining[0].Token, keep)
	}
}
