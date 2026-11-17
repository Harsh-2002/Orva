package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSessionIdentityWriterAndCommitRevalidation(t *testing.T) {
	db := newTestDB(t)
	userID := sessionIdentityUser(t, db)
	legacy, err := db.CreateSession(userID, time.Hour)
	if err != nil || legacy.ID != "" {
		t.Fatal("legacy schema changed session creation")
	}
	if err := db.initSessionIdentitySchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := db.CreateSession(userID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateSession(userID, time.Hour)
	if err != nil || !validIAMUUID(first.ID) || !validIAMUUID(second.ID) || first.ID == second.ID {
		t.Fatal("session identities are not independent UUIDs")
	}
	body, err := json.Marshal(first)
	if err != nil || strings.Contains(string(body), first.ID) {
		t.Fatal("session API payload disclosed internal identity")
	}
	ref := OperatorCredentialReference{Kind: "session", ID: first.ID}
	assertAuthorization := func(wantDenied bool) {
		t.Helper()
		tx, err := db.write.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		actor, _, err := authorizeOperatorTx(context.Background(), tx, ref)
		if wantDenied {
			if !errors.Is(err, ErrOperatorDenied) {
				t.Fatal("stale captured reference retained commit authority")
			}
		} else if err != nil || actor.Kind != "session" || actor.ID != first.ID {
			t.Fatal("new session identity did not authorize as itself")
		}
	}
	assertAuthorization(false)
	if err := db.DeleteSession(first.Token); err != nil {
		t.Fatal(err)
	}
	assertAuthorization(true)
	if _, err := db.ResolveSessionIdentityByID(context.Background(), second.ID); err != nil {
		t.Fatal("revoking one device affected another identity")
	}
}

func TestSessionIdentityWriterFailureIsPrivateAndAtomic(t *testing.T) {
	db := newTestDB(t)
	userID := sessionIdentityUser(t, db)
	if err := db.initSessionIdentitySchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	const diagnostic = "private-trigger-diagnostic"
	if _, err := db.write.Exec(`CREATE TRIGGER fail_session_insert BEFORE INSERT ON sessions
		BEGIN SELECT RAISE(ABORT, 'private-trigger-diagnostic'); END`); err != nil {
		t.Fatal(err)
	}
	session, err := db.CreateSession(userID, time.Hour)
	if session != nil || !errors.Is(err, ErrSessionIdentityUnavailable) || strings.Contains(err.Error(), diagnostic) {
		t.Fatal("failed session creation disclosed diagnostics or returned an unusable identity")
	}
	var count int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed session creation left a partial row")
	}
}
