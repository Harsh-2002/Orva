package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/internal/ids"
	"modernc.org/sqlite"
)

func sessionIdentityUser(t *testing.T, db *Database) int64 {
	t.Helper()
	result, err := db.write.Exec(`INSERT INTO users (username,password_hash) VALUES ('identity-user','unused-test-hash')`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func sessionIdentitySeed(t *testing.T, db *Database, userID int64, count int) []string {
	t.Helper()
	tx, err := db.write.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	tokens := make([]string, count)
	for i := range tokens {
		tokens[i] = fmt.Sprintf("private-test-bearer-%064d", i)
		if _, err := tx.Exec(`INSERT INTO sessions (token,user_id,created_at,expires_at) VALUES (?,?,?,?)`,
			tokens[i], userID, time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour)); err != nil {
			t.Fatal("insert session fixture failed")
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return tokens
}

func sessionIdentityHasColumn(t *testing.T, db *Database) bool {
	t.Helper()
	var n int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='id'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n != 0
}

func sessionIdentityAssertDenied(t *testing.T, db *Database, token, id string) {
	t.Helper()
	record, err := db.ResolveSessionIdentityByToken(context.Background(), token)
	if record != nil || !errors.Is(err, ErrSessionIdentityUnavailable) {
		t.Fatal("token lookup did not fail closed")
	}
	if strings.Contains(err.Error(), token) && token != "" {
		t.Fatal("token lookup disclosed a bearer")
	}
	record, err = db.ResolveSessionIdentityByID(context.Background(), id)
	if record != nil || !errors.Is(err, ErrSessionIdentityUnavailable) {
		t.Fatal("ID lookup did not fail closed")
	}
}

func TestSessionIdentityExplicitSchemaOnly(t *testing.T) {
	db := newTestDB(t)
	userID := sessionIdentityUser(t, db)
	tokens := sessionIdentitySeed(t, db, userID, 1)
	if sessionIdentityHasColumn(t, db) {
		t.Fatal("normal migration initialized session IDs")
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if sessionIdentityHasColumn(t, db) {
		t.Fatal("repeat migration initialized session IDs")
	}
	sessionIdentityAssertDenied(t, db, tokens[0], ids.New())
	if err := db.initSessionIdentitySchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"workload_iam_policies", "workload_iam_audit", "workload_artifact_generations"} {
		var n int
		if err := db.read.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name=?`, table).Scan(&n); err != nil || n != 0 {
			t.Fatal("session initialization activated an unrelated schema")
		}
	}
	legacy, err := db.CreateSession(userID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var id sql.NullString
	if err := db.read.QueryRow(`SELECT id FROM sessions WHERE token=?`, legacy.Token).Scan(&id); err != nil || id.Valid {
		t.Fatal("legacy writer unexpectedly assigned an identity")
	}
	sessionIdentityAssertDenied(t, db, legacy.Token, ids.New())
	if user, err := db.GetSessionUser(legacy.Token); err != nil || user.ID != userID {
		t.Fatal("legacy session authentication changed")
	}
}

func TestSessionIdentityBackfillPreservesAndPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	userID := sessionIdentityUser(t, db)
	tokens := sessionIdentitySeed(t, db, userID, 2*sessionIdentityBatchSize+3)
	before := make([]*Session, len(tokens))
	for i, token := range tokens {
		before[i], err = db.GetSession(token)
		if err != nil {
			t.Fatal("read original session failed")
		}
	}
	stableID := ids.New()
	if _, err := db.write.Exec(`ALTER TABLE sessions ADD COLUMN id TEXT`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`UPDATE sessions SET id=? WHERE token=?`, stableID, tokens[0]); err != nil {
		t.Fatal(err)
	}
	if err := db.initSessionIdentitySchema(ctx); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(tokens))
	identities := make([]*SessionIdentity, len(tokens))
	for i, token := range tokens {
		after, err := db.GetSession(token)
		if err != nil || !reflect.DeepEqual(before[i], after) {
			t.Fatal("backfill changed legacy session data")
		}
		s, err := db.ResolveSessionIdentityByToken(ctx, token)
		if err != nil || s == nil {
			t.Fatal("backfilled identity unavailable")
		}
		if !validIAMUUID(s.ID) || s.ID[14] != '7' || seen[s.ID] || strings.Contains(token, s.ID) {
			t.Fatal("backfill did not assign independent unique canonical host UUIDs")
		}
		seen[s.ID] = true
		if s.UserID != userID || s.Username != "identity-user" || !s.ExpiresAt.Equal(before[i].ExpiresAt) || s.ExpiresAt.Location() != time.UTC {
			t.Fatal("identity provenance differs from session")
		}
		if err := validateIAMActor(IAMActorReference{Kind: "session", ID: s.ID}); err != nil {
			t.Fatal("identity incompatible with IAM actor references")
		}
		data, err := json.Marshal(s)
		if err != nil || strings.Contains(string(data), token) || strings.Contains(fmt.Sprint(s), token) {
			t.Fatal("identity record disclosed a bearer")
		}
		identities[i] = s
	}
	if identities[0].ID != stableID {
		t.Fatal("initializer replaced an existing stable identity")
	}
	if reflect.TypeOf(SessionIdentity{}).NumField() != 4 {
		t.Fatal("identity data contract changed")
	}
	if err := db.initSessionIdentitySchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.initSessionIdentitySchema(ctx); err != nil {
		t.Fatal(err)
	}
	for i, expected := range identities {
		byID, err := db.ResolveSessionIdentityByID(ctx, expected.ID)
		if err != nil || !reflect.DeepEqual(expected, byID) {
			t.Fatal("repeat initialization/reopen changed an identity")
		}
		byToken, err := db.ResolveSessionIdentityByToken(ctx, tokens[i])
		if err != nil || !reflect.DeepEqual(expected, byToken) {
			t.Fatal("reopened token and ID lookups disagree")
		}
	}
	if _, err := db.write.Exec(`UPDATE sessions SET id=? WHERE token=?`, identities[0].ID, tokens[1]); err == nil {
		t.Fatal("identity uniqueness not enforced")
	}
}

func TestSessionIdentityInvalidExistingIDsRollback(t *testing.T) {
	for _, bad := range []string{"", "private-test-bearer", strings.ToUpper(iamTestOwner), strings.ReplaceAll(iamTestOwner, "-", ""),
		"00000000-0000-0000-0000-000000000000", "019a8000-0000-0000-8000-000000000001", "019a8000-0000-7000-0000-000000000001"} {
		t.Run(fmt.Sprintf("case-%d", len(bad)), func(t *testing.T) {
			db := newTestDB(t)
			tokens := sessionIdentitySeed(t, db, sessionIdentityUser(t, db), sessionIdentityBatchSize+1)
			if _, err := db.write.Exec(`ALTER TABLE sessions ADD COLUMN id TEXT`); err != nil {
				t.Fatal(err)
			}
			stableID := ids.New()
			if _, err := db.write.Exec(`UPDATE sessions SET id=? WHERE token=?`, stableID, tokens[0]); err != nil {
				t.Fatal(err)
			}
			if _, err := db.write.Exec(`UPDATE sessions SET id=? WHERE token=?`, bad, tokens[len(tokens)-1]); err != nil {
				t.Fatal(err)
			}
			if err := db.initSessionIdentitySchema(context.Background()); err != ErrSessionIdentityUnavailable {
				t.Fatal("malformed existing ID not rejected generically")
			}
			var count int
			if err := db.read.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id IS NULL`).Scan(&count); err != nil || count != len(tokens)-2 {
				t.Fatal("failed validation partially backfilled IDs")
			}
			var preserved string
			if err := db.read.QueryRow(`SELECT id FROM sessions WHERE token=?`, tokens[0]).Scan(&preserved); err != nil || preserved != stableID {
				t.Fatal("existing stable ID overwritten")
			}
			if err := db.read.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='idx_sessions_identity'`).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed validation committed the index")
			}
			sessionIdentityAssertDenied(t, db, tokens[len(tokens)-1], bad)
		})
	}
}

func TestSessionIdentityDuplicateIDsReject(t *testing.T) {
	db := newTestDB(t)
	tokens := sessionIdentitySeed(t, db, sessionIdentityUser(t, db), 3)
	if _, err := db.write.Exec(`ALTER TABLE sessions ADD COLUMN id TEXT`); err != nil {
		t.Fatal(err)
	}
	id := ids.New()
	if _, err := db.write.Exec(`UPDATE sessions SET id=? WHERE token IN (?,?)`, id, tokens[1], tokens[2]); err != nil {
		t.Fatal(err)
	}
	if err := db.initSessionIdentitySchema(context.Background()); err != ErrSessionIdentityUnavailable {
		t.Fatal("duplicate IDs not rejected generically")
	}
	var count int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, id).Scan(&count); err != nil || count != 2 {
		t.Fatal("duplicate stable IDs overwritten")
	}
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id IS NULL`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate rejection partially backfilled IDs")
	}
	sessionIdentityAssertDenied(t, db, tokens[1], id)
}

func TestSessionIdentityIndexNameCollisionRejects(t *testing.T) {
	db := newTestDB(t)
	sessionIdentitySeed(t, db, sessionIdentityUser(t, db), 1)
	if _, err := db.write.Exec(`CREATE INDEX idx_sessions_identity ON sessions(user_id)`); err != nil {
		t.Fatal(err)
	}
	if err := db.initSessionIdentitySchema(context.Background()); err != ErrSessionIdentityUnavailable {
		t.Fatal("unrelated same-named index was accepted")
	}
	if sessionIdentityHasColumn(t, db) {
		t.Fatal("invalid index committed partial schema")
	}
}

func TestSessionIdentityLiveRevalidation(t *testing.T) {
	for _, mutation := range []string{"expired", "logout", "user-deleted", "orphaned", "malformed", "missing"} {
		t.Run(mutation, func(t *testing.T) {
			db := newTestDB(t)
			userID := sessionIdentityUser(t, db)
			token := sessionIdentitySeed(t, db, userID, 1)[0]
			if err := db.initSessionIdentitySchema(context.Background()); err != nil {
				t.Fatal(err)
			}
			s, err := db.ResolveSessionIdentityByToken(context.Background(), token)
			if err != nil {
				t.Fatal(err)
			}
			sessionIdentityAssertDenied(t, db, "unknown-private-bearer", ids.New())
			sessionIdentityAssertDenied(t, db, "", "invalid-id")
			switch mutation {
			case "expired":
				_, err = db.write.Exec(`UPDATE sessions SET expires_at=? WHERE token=?`, time.Now().UTC().Add(-time.Hour), token)
			case "logout":
				err = db.DeleteSession(token)
			case "user-deleted":
				_, err = db.write.Exec(`DELETE FROM users WHERE id=?`, userID)
			case "orphaned":
				conn, connErr := db.write.Conn(context.Background())
				if connErr != nil {
					t.Fatal(connErr)
				}
				if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=OFF`); err != nil {
					conn.Close()
					t.Fatal(err)
				}
				_, err = conn.ExecContext(context.Background(), `UPDATE sessions SET user_id=? WHERE token=?`, userID+1000, token)
				_, restoreErr := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
				conn.Close()
				if restoreErr != nil {
					t.Fatal(restoreErr)
				}
			case "malformed":
				_, err = db.write.Exec(`UPDATE sessions SET id='malformed' WHERE token=?`, token)
			case "missing":
				_, err = db.write.Exec(`UPDATE sessions SET id=NULL WHERE token=?`, token)
			}
			if err != nil {
				t.Fatal("session mutation fixture failed")
			}
			sessionIdentityAssertDenied(t, db, token, s.ID)
		})
	}
}

func TestSessionIdentityBackfillFailureRollsBackDDL(t *testing.T) {
	db := newTestDB(t)
	tokens := sessionIdentitySeed(t, db, sessionIdentityUser(t, db), sessionIdentityBatchSize+1)
	if _, err := db.write.Exec(fmt.Sprintf(`CREATE TRIGGER fail_identity_backfill BEFORE UPDATE ON sessions
		WHEN NEW.rowid=%d BEGIN SELECT RAISE(ABORT, '%s'); END`, len(tokens), tokens[0])); err != nil {
		t.Fatal("create failure fixture failed")
	}
	err := db.initSessionIdentitySchema(context.Background())
	if err != ErrSessionIdentityUnavailable || strings.Contains(err.Error(), tokens[0]) {
		t.Fatal("backfill failure not sanitized")
	}
	if sessionIdentityHasColumn(t, db) {
		t.Fatal("failed backfill committed the new column")
	}
	var count int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil || count != len(tokens) {
		t.Fatal("failed backfill lost legacy sessions")
	}
	if _, err := db.write.Exec(`DROP TRIGGER fail_identity_backfill`); err != nil {
		t.Fatal(err)
	}
	if err := db.initSessionIdentitySchema(context.Background()); err != nil {
		t.Fatal("backfill could not retry after rollback")
	}
}

func TestSessionIdentityCancellation(t *testing.T) {
	db := newTestDB(t)
	token := sessionIdentitySeed(t, db, sessionIdentityUser(t, db), 1)[0]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.initSessionIdentitySchema(ctx); !errors.Is(err, context.Canceled) || !errors.Is(err, ErrSessionIdentityUnavailable) {
		t.Fatal("initializer lost cancellation")
	}
	if sessionIdentityHasColumn(t, db) {
		t.Fatal("canceled initializer committed schema")
	}
	if err := db.initSessionIdentitySchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := db.ResolveSessionIdentityByToken(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	for _, lookup := range []func() (*SessionIdentity, error){
		func() (*SessionIdentity, error) { return db.ResolveSessionIdentityByToken(ctx, token) },
		func() (*SessionIdentity, error) { return db.ResolveSessionIdentityByID(ctx, s.ID) },
	} {
		record, err := lookup()
		if record != nil || !errors.Is(err, context.Canceled) || !errors.Is(err, ErrSessionIdentityUnavailable) {
			t.Fatal("canceled lookup did not fail closed")
		}
	}
}

func TestSessionIdentityCancellationDuringBackfill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	function := "test_cancel_identity_" + strings.ReplaceAll(ids.New(), "-", "")
	invoked := false
	if err := sqlite.RegisterScalarFunction(function, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		invoked = true
		cancel()
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	db := newTestDB(t)
	tokens := sessionIdentitySeed(t, db, sessionIdentityUser(t, db), sessionIdentityBatchSize+1)
	if _, err := db.write.Exec(fmt.Sprintf(`CREATE TRIGGER cancel_identity_backfill BEFORE UPDATE ON sessions
		WHEN NEW.rowid=%d BEGIN SELECT %s(); END`, len(tokens), function)); err != nil {
		t.Fatal("create cancellation fixture failed")
	}
	err := db.initSessionIdentitySchema(ctx)
	if !invoked || !errors.Is(err, context.Canceled) || !errors.Is(err, ErrSessionIdentityUnavailable) {
		t.Fatal("backfill did not honor cancellation after its first batch")
	}
	if sessionIdentityHasColumn(t, db) {
		t.Fatal("canceled backfill committed partial schema/data")
	}
	if _, err := db.write.Exec(`DROP TRIGGER cancel_identity_backfill`); err != nil {
		t.Fatal(err)
	}
	if err := db.initSessionIdentitySchema(context.Background()); err != nil {
		t.Fatal("backfill could not retry after cancellation")
	}
}
