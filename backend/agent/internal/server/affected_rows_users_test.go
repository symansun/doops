package server

// INT-86: SetUserAdmin and SetUserPassword used to read MySQL's
// changed-rows count as proof of existence, so a caller repeating the same
// value (is_admin true twice, or a repeatable hash) would get "user not found"
// for a user that exists. Both now re-check existence in the same transaction.
//
// SQLite matches rows and therefore cannot reproduce the defect; the MySQL
// half of this file runs against a real server through the same
// DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT gate as affected_rows_mysql_test.go.

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func readUserIsAdmin(t *testing.T, store *GatewayStore, id string) int {
	t.Helper()
	var admin int
	if err := store.db.QueryRow(`SELECT is_admin FROM users WHERE id = ?`, id).Scan(&admin); err != nil {
		t.Fatalf("read is_admin for %s: %v", id, err)
	}
	return admin
}

func deleteUser(t *testing.T, store *GatewayStore, id string) {
	t.Helper()
	if _, err := store.db.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		t.Fatalf("delete user %s: %v", id, err)
	}
}

// TestSetUserAdminRepeatedCallSucceeds pins the contract on SQLite:
// repeating the same is_admin value is a no-op success, and only a truly
// missing user reports "user not found".
func TestSetUserAdminRepeatedCallSucceeds(t *testing.T) {
	store := newAffectedRowsSQLiteStore(t)
	user, err := store.CreateUser("admin-repeat")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := store.SetUserAdmin(user.ID, true); err != nil {
			t.Fatalf("SetUserAdmin(true) call %d: %v", i+1, err)
		}
	}
	if got := readUserIsAdmin(t, store, user.ID); got != 1 {
		t.Fatalf("is_admin after two SetUserAdmin(true) calls = %d, want 1", got)
	}
	for i := 0; i < 2; i++ {
		if err := store.SetUserAdmin(user.ID, false); err != nil {
			t.Fatalf("SetUserAdmin(false) call %d: %v", i+1, err)
		}
	}
	if got := readUserIsAdmin(t, store, user.ID); got != 0 {
		t.Fatalf("is_admin after two SetUserAdmin(false) calls = %d, want 0", got)
	}

	err = store.SetUserAdmin("usr_missing", true)
	if err == nil || !strings.Contains(err.Error(), "user not found") {
		t.Fatalf("SetUserAdmin of a missing user = %v, want the original \"user not found\" error", err)
	}
}

// TestSetUserPasswordRepeatedCallSucceeds pins that changing the password
// twice succeeds, that a disabled account still reports sql.ErrNoRows, and
// that a missing account keeps the same error.
func TestSetUserPasswordRepeatedCallSucceeds(t *testing.T) {
	store := newAffectedRowsSQLiteStore(t)
	user, err := store.CreateUserWithPassword(CreateUserRequest{Name: "pw-repeat", Password: "first-password"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := store.SetUserPassword(user.ID, "second-password"); err != nil {
			t.Fatalf("SetUserPassword call %d: %v", i+1, err)
		}
	}

	if err := store.SetUserDisabled(user.ID, true); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if err := store.SetUserPassword(user.ID, "third-password"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("SetUserPassword for a disabled user = %v, want sql.ErrNoRows", err)
	}
	if err := store.SetUserPassword("usr_missing", "whatever"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("SetUserPassword for a missing user = %v, want sql.ErrNoRows", err)
	}
}

// TestAffectedRowsMySQLSetUserAdminRepeatedCallSucceeds covers the admin flag on
// a real MySQL, where repeating a value reports 0 changed rows.
func TestAffectedRowsMySQLSetUserAdminRepeatedCallSucceeds(t *testing.T) {
	store := openAffectedRowsMySQLStore(t)
	ctx := context.Background()
	user, err := store.CreateUser("admin-repeat")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := store.db.ExecContext(ctx, `UPDATE users SET is_admin = ? WHERE id = ?`, true, user.ID); err != nil {
		t.Fatalf("seed is_admin: %v", err)
	}
	assertSameValueUpdateReportsZero(t, ctx, store, "users is_admin (already 1)",
		`UPDATE users SET is_admin = ? WHERE id = ?`, true, user.ID)

	for i := 0; i < 2; i++ {
		if err := store.SetUserAdmin(user.ID, true); err != nil {
			t.Fatalf("SetUserAdmin(true) call %d on MySQL: %v", i+1, err)
		}
	}
	if got := readUserIsAdmin(t, store, user.ID); got != 1 {
		t.Fatalf("is_admin after two SetUserAdmin(true) calls on MySQL = %d, want 1", got)
	}

	deleteUser(t, store, user.ID)
	err = store.SetUserAdmin(user.ID, true)
	if err == nil || !strings.Contains(err.Error(), "user not found") {
		t.Fatalf("SetUserAdmin of a deleted user = %v, want the original \"user not found\" error", err)
	}
}

// TestAffectedRowsMySQLSetUserPasswordRepeatedValueSucceeds covers the hash
// column on a real MySQL, plus the unchanged contract for missing and
// disabled accounts.
func TestAffectedRowsMySQLSetUserPasswordRepeatedValueSucceeds(t *testing.T) {
	store := openAffectedRowsMySQLStore(t)
	ctx := context.Background()
	user, err := store.CreateUserWithPassword(CreateUserRequest{Name: "pw-repeat", Password: "first-password"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	const literalHash = "not-a-bcrypt-hash-but-a-stable-value"
	if _, err := store.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, literalHash, user.ID); err != nil {
		t.Fatalf("seed password_hash: %v", err)
	}
	assertSameValueUpdateReportsZero(t, ctx, store, "users password_hash (already the same value)",
		`UPDATE users SET password_hash = ? WHERE id = ? AND disabled = 0`, literalHash, user.ID)

	for i := 0; i < 2; i++ {
		if err := store.SetUserPassword(user.ID, "second-password"); err != nil {
			t.Fatalf("SetUserPassword call %d on MySQL: %v", i+1, err)
		}
	}

	if err := store.SetUserDisabled(user.ID, true); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if err := store.SetUserPassword(user.ID, "third-password"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("SetUserPassword for a disabled user on MySQL = %v, want sql.ErrNoRows", err)
	}

	deleteUser(t, store, user.ID)
	if err := store.SetUserPassword(user.ID, "fourth-password"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("SetUserPassword for a deleted user on MySQL = %v, want sql.ErrNoRows", err)
	}
}
