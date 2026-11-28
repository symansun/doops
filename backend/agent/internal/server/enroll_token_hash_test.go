package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// INT-69 acceptance cases. agent_enrollments and edge_enrollments now use the
// same two-generation hash scheme INT-64 landed for tokens: new rows store
// "sha256:<64 hex>", a legacy bcrypt row is upgraded in the request that first
// verifies it, and a failed verification never upgrades. Every case runs
// against a real OpenGatewayStore (real tables, real SQL, the real
// Verify*Enrollment functions), and both tables are exercised separately - the
// edge implementation is never inferred from the agent one.

type enrollFixture struct {
	name      string
	unknown   string
	hashTable enrollmentHashTable
	// issue returns the row id and the plaintext shown once at issuance.
	issue  func(t *testing.T, store *GatewayStore, cluster, instance string) (id, plain string)
	verify func(store *GatewayStore, plain, cluster, instance string) (id string, err error)
	revoke func(store *GatewayStore, id string) error
	// confirm is nil when the table has no confirmed_at column (edge).
	confirm func(store *GatewayStore, id string) error
}

func enrollFixtures(t *testing.T) []enrollFixture {
	t.Helper()
	return []enrollFixture{
		{
			name:      "agent",
			unknown:   "dga_aen_aen_deadbeefcafe_nope",
			hashTable: agentEnrollmentHashTable,
			issue: func(t *testing.T, store *GatewayStore, cluster, instance string) (string, string) {
				t.Helper()
				created, err := store.CreateAgentEnrollment(cluster, instance)
				if err != nil {
					t.Fatalf("create agent enrollment: %v", err)
				}
				return created.ID, created.Plaintext
			},
			verify: func(store *GatewayStore, plain, cluster, instance string) (string, error) {
				got, err := store.VerifyAgentEnrollment(plain, cluster, instance)
				return got.ID, err
			},
			revoke:  func(store *GatewayStore, id string) error { return store.RevokeAgentEnrollment(id) },
			confirm: func(store *GatewayStore, id string) error { return store.ConfirmAgentEnrollment(id) },
		},
		{
			name:      "edge",
			unknown:   "dge_een_een_deadbeefcafe_nope",
			hashTable: edgeEnrollmentHashTable,
			issue: func(t *testing.T, store *GatewayStore, cluster, instance string) (string, string) {
				t.Helper()
				created, err := store.CreateEdgeEnrollment(cluster, instance)
				if err != nil {
					t.Fatalf("create edge enrollment: %v", err)
				}
				return created.ID, created.Plaintext
			},
			verify: func(store *GatewayStore, plain, cluster, instance string) (string, error) {
				got, err := store.VerifyEdgeEnrollment(plain, cluster, instance)
				return got.ID, err
			},
			revoke: func(store *GatewayStore, id string) error { return store.RevokeEdgeEnrollment(id) },
		},
	}
}

// enrollRecordSelect reads the seven columns both enrollment tables share. The
// upgrade statement only ever writes hash, so comparing this string before and
// after a rejected verification is a byte-for-byte check of the decision inputs.
func enrollRecord(t *testing.T, store *GatewayStore, fx enrollFixture, id string) string {
	t.Helper()
	var query string
	switch fx.name {
	case "agent":
		query = `SELECT id, cluster, instance, prefix, hash, revoked_at, created_at FROM agent_enrollments WHERE id = ?`
	case "edge":
		query = `SELECT id, cluster, instance, prefix, hash, revoked_at, created_at FROM edge_enrollments WHERE id = ?`
	default:
		t.Fatalf("unknown enrollment fixture %q", fx.name)
	}
	var rowID, cluster, instance, prefix, hash, revoked, created string
	if err := store.db.QueryRow(query, id).Scan(&rowID, &cluster, &instance, &prefix, &hash, &revoked, &created); err != nil {
		t.Fatalf("read %s row %s: %v", fx.name, id, err)
	}
	return strings.Join([]string{rowID, cluster, instance, prefix, hash, revoked, created}, "\x00")
}

func enrollHash(t *testing.T, store *GatewayStore, fx enrollFixture, id string) string {
	t.Helper()
	var query string
	switch fx.name {
	case "agent":
		query = `SELECT hash FROM agent_enrollments WHERE id = ?`
	case "edge":
		query = `SELECT hash FROM edge_enrollments WHERE id = ?`
	default:
		t.Fatalf("unknown enrollment fixture %q", fx.name)
	}
	var hash string
	if err := store.db.QueryRow(query, id).Scan(&hash); err != nil {
		t.Fatalf("read %s hash for %s: %v", fx.name, id, err)
	}
	return hash
}

func writeEnrollHash(t *testing.T, store *GatewayStore, fx enrollFixture, id, hash string) {
	t.Helper()
	var stmt string
	switch fx.name {
	case "agent":
		stmt = `UPDATE agent_enrollments SET hash = ? WHERE id = ?`
	case "edge":
		stmt = `UPDATE edge_enrollments SET hash = ? WHERE id = ?`
	default:
		t.Fatalf("unknown enrollment fixture %q", fx.name)
	}
	if _, err := store.db.Exec(stmt, hash, id); err != nil {
		t.Fatalf("write %s hash for %s: %v", fx.name, id, err)
	}
}

// presetLegacyEnrollHash turns a freshly issued row into the pre-INT-69 shape: a
// real bcrypt hash over tokenPassword(plaintext), exactly what the old
// Create*Enrollment wrote.
func presetLegacyEnrollHash(t *testing.T, store *GatewayStore, fx enrollFixture, id, plain string) string {
	t.Helper()
	legacy, err := bcrypt.GenerateFromPassword(tokenPassword(plain), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	writeEnrollHash(t, store, fx, id, string(legacy))
	return string(legacy)
}

// installEnrollWriteCounter counts real hash rewrites with a SQLite trigger, so
// the concurrency case observes the database, not the return value of the
// compare-and-set statement. It must run after the legacy hash was written:
// only UPDATEs that change hash are counted.
func installEnrollWriteCounter(t *testing.T, store *GatewayStore, fx enrollFixture) {
	t.Helper()
	var trigger string
	switch fx.name {
	case "agent":
		trigger = `CREATE TRIGGER enroll_hash_write_agent AFTER UPDATE OF hash ON agent_enrollments
			WHEN OLD.hash <> NEW.hash
			BEGIN UPDATE enroll_hash_write_counter SET n = n + 1; END`
	case "edge":
		trigger = `CREATE TRIGGER enroll_hash_write_edge AFTER UPDATE OF hash ON edge_enrollments
			WHEN OLD.hash <> NEW.hash
			BEGIN UPDATE enroll_hash_write_counter SET n = n + 1; END`
	default:
		t.Fatalf("unknown enrollment fixture %q", fx.name)
	}
	for _, stmt := range []string{
		`CREATE TABLE enroll_hash_write_counter (n INTEGER NOT NULL)`,
		`INSERT INTO enroll_hash_write_counter (n) VALUES (0)`,
		trigger,
	} {
		if _, err := store.db.Exec(stmt); err != nil {
			t.Fatalf("install %s hash write counter: %v", fx.name, err)
		}
	}
}

func enrollHashWriteCount(t *testing.T, store *GatewayStore) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(`SELECT n FROM enroll_hash_write_counter`).Scan(&n); err != nil {
		t.Fatalf("read hash write counter: %v", err)
	}
	return n
}

func requireEnrollUnauthorized(t *testing.T, fx enrollFixture, id string, err error) {
	t.Helper()
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("%s enrollment %s: err = %v, want ErrUnauthorized", fx.name, id, err)
	}
}

// 1: a newly issued enrollment is stored in the fast form and still verifies.
func TestEnrollTokenHashNewRowUsesFastHash(t *testing.T) {
	for _, fx := range enrollFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			store := openTokenHashStore(t)
			id, plain := fx.issue(t, store, "demo", "gw-demo")

			hash := enrollHash(t, store, fx, id)
			if !strings.HasPrefix(hash, tokenFastHashPrefix) {
				t.Fatalf("issued %s hash %q is not in the sha256 form", fx.name, hash)
			}
			sum := sha256.Sum256([]byte(plain))
			if want := "sha256:" + hex.EncodeToString(sum[:]); hash != want {
				t.Fatalf("issued %s hash = %q, want %q", fx.name, hash, want)
			}
			if len(hash) != len("sha256:")+64 {
				t.Fatalf("issued %s hash %q is not 64 hex characters after the prefix", fx.name, hash)
			}

			gotID, err := fx.verify(store, plain, "demo", "gw-demo")
			if err != nil {
				t.Fatalf("fast %s token rejected: %v", fx.name, err)
			}
			if gotID != id {
				t.Fatalf("verified %s id = %s, want %s", fx.name, gotID, id)
			}
		})
	}
}

// 2: one changed character is invalid on both hash generations, and the row is
// not rewritten by the rejection.
func TestEnrollTokenHashTamperedIsInvalid(t *testing.T) {
	for _, fx := range enrollFixtures(t) {
		t.Run(fx.name+"/fast", func(t *testing.T) {
			store := openTokenHashStore(t)
			id, plain := fx.issue(t, store, "demo", "gw-demo")
			before := enrollHash(t, store, fx, id)

			_, err := fx.verify(store, flipLastChar(plain), "demo", "gw-demo")
			requireEnrollUnauthorized(t, fx, id, err)
			if after := enrollHash(t, store, fx, id); after != before {
				t.Fatalf("tampered fast %s token rewrote the row: %q -> %q", fx.name, before, after)
			}
		})
		t.Run(fx.name+"/legacy", func(t *testing.T) {
			store := openTokenHashStore(t)
			id, plain := fx.issue(t, store, "demo", "gw-demo")
			presetLegacyEnrollHash(t, store, fx, id, plain)

			_, err := fx.verify(store, flipLastChar(plain), "demo", "gw-demo")
			requireEnrollUnauthorized(t, fx, id, err)
		})
	}
}

// 3: a pre-existing bcrypt row keeps working and is upgraded in the same
// request; the second verification takes the fast path and rewrites nothing.
func TestEnrollTokenHashLegacyRowUpgradesOnFirstUse(t *testing.T) {
	for _, fx := range enrollFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			store := openTokenHashStore(t)
			id, plain := fx.issue(t, store, "demo", "gw-demo")
			legacy := presetLegacyEnrollHash(t, store, fx, id, plain)
			if !strings.HasPrefix(legacy, "$2") || !isLegacyTokenHash(legacy) {
				t.Fatalf("fixture is not a bcrypt hash: %q", legacy)
			}
			if isTokenFastHash(legacy) {
				t.Fatalf("fixture is already the fast form: %q", legacy)
			}

			gotID, err := fx.verify(store, plain, "demo", "gw-demo")
			if err != nil {
				t.Fatalf("legacy %s token rejected: %v", fx.name, err)
			}
			if gotID != id {
				t.Fatalf("verified %s id = %s, want %s", fx.name, gotID, id)
			}
			upgraded := enrollHash(t, store, fx, id)
			if !isTokenFastHash(upgraded) {
				t.Fatalf("legacy %s hash was not upgraded, still %q", fx.name, upgraded)
			}
			if want := tokenFastHash(plain); upgraded != want {
				t.Fatalf("upgraded %s hash = %q, want %q", fx.name, upgraded, want)
			}
			// The statement is a compare-and-set on the legacy hash: replaying it
			// must change zero rows, so a concurrent stale writer cannot win.
			if n := store.upgradeEnrollmentHash(fx.hashTable, id, legacy, tokenFastHash(plain)); n != 0 {
				t.Fatalf("stale compare-and-set changed %d %s rows, want 0", n, fx.name)
			}

			secondID, err := fx.verify(store, plain, "demo", "gw-demo")
			if err != nil {
				t.Fatalf("upgraded %s token rejected: %v", fx.name, err)
			}
			if secondID != gotID {
				t.Fatalf("identity changed across the upgrade: %s then %s", gotID, secondID)
			}
			if again := enrollHash(t, store, fx, id); again != upgraded {
				t.Fatalf("second verification rewrote the %s hash: %q then %q", fx.name, upgraded, again)
			}
		})
	}
}

// 4: a failed verification leaves the row byte-for-byte untouched - the upgrade
// happens after the comparison, never before it.
func TestEnrollTokenHashFailedVerificationDoesNotUpgrade(t *testing.T) {
	for _, fx := range enrollFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			store := openTokenHashStore(t)
			id, plain := fx.issue(t, store, "demo", "gw-demo")
			legacy := presetLegacyEnrollHash(t, store, fx, id, plain)
			before := enrollRecord(t, store, fx, id)

			_, err := fx.verify(store, flipLastChar(plain), "demo", "gw-demo")
			requireEnrollUnauthorized(t, fx, id, err)
			if after := enrollRecord(t, store, fx, id); after != before {
				t.Fatalf("failed verification changed the %s row: before %q after %q", fx.name, before, after)
			}
			if hash := enrollHash(t, store, fx, id); hash != legacy {
				t.Fatalf("failed verification upgraded the %s hash: %q -> %q", fx.name, legacy, hash)
			}

			// The genuine token still verifies afterwards and then upgrades.
			if _, err := fx.verify(store, plain, "demo", "gw-demo"); err != nil {
				t.Fatalf("genuine %s token rejected after a failed attempt: %v", fx.name, err)
			}
			if hash := enrollHash(t, store, fx, id); hash != tokenFastHash(plain) {
				t.Fatalf("genuine verification did not upgrade the %s hash: %q", fx.name, hash)
			}
		})
	}
}

// 5: revocation is re-read on every call and rejects the token before any hash
// comparison, so a revoked legacy row is never upgraded.
func TestEnrollTokenHashRevokedRowRejectedWithoutUpgrade(t *testing.T) {
	for _, fx := range enrollFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			store := openTokenHashStore(t)
			id, plain := fx.issue(t, store, "demo", "gw-demo")
			legacy := presetLegacyEnrollHash(t, store, fx, id, plain)
			if err := fx.revoke(store, id); err != nil {
				t.Fatalf("revoke %s enrollment: %v", fx.name, err)
			}

			_, err := fx.verify(store, plain, "demo", "gw-demo")
			requireEnrollUnauthorized(t, fx, id, err)
			// Second call: the decision is re-read from the database, not cached.
			_, err = fx.verify(store, plain, "demo", "gw-demo")
			requireEnrollUnauthorized(t, fx, id, err)
			if hash := enrollHash(t, store, fx, id); hash != legacy {
				t.Fatalf("revoked %s row was upgraded: %q -> %q", fx.name, legacy, hash)
			}
		})
	}
}

// 6: unknown tokens are rejected before any row is touched.
func TestEnrollTokenHashUnknownTokenRejected(t *testing.T) {
	for _, fx := range enrollFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			store := openTokenHashStore(t)
			// A live row proves the store is working; the unknown tokens below must
			// still be refused.
			id, plain := fx.issue(t, store, "demo", "gw-demo")
			if _, err := fx.verify(store, plain, "demo", "gw-demo"); err != nil {
				t.Fatalf("issued %s token rejected: %v", fx.name, err)
			}

			for _, token := range []string{"not-an-enrollment-token", fx.unknown} {
				if _, err := fx.verify(store, token, "demo", "gw-demo"); !errors.Is(err, ErrUnauthorized) {
					t.Fatalf("%s unknown token %q: err = %v, want ErrUnauthorized", fx.name, token, err)
				}
			}
			if hash := enrollHash(t, store, fx, id); hash != tokenFastHash(plain) {
				t.Fatalf("unknown token attempts changed the live %s row: %q", fx.name, hash)
			}
		})
	}
}

// 7: an enrollment bound to another cluster/instance is rejected before the
// comparison, so it cannot be upgraded by a wrong-target attempt.
func TestEnrollTokenHashBoundToAnotherTargetRejected(t *testing.T) {
	for _, fx := range enrollFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			store := openTokenHashStore(t)
			id, plain := fx.issue(t, store, "demo", "gw-demo")
			legacy := presetLegacyEnrollHash(t, store, fx, id, plain)

			for _, target := range [][2]string{{"demo", "other"}, {"other", "gw-demo"}} {
				_, err := fx.verify(store, plain, target[0], target[1])
				requireEnrollUnauthorized(t, fx, id, err)
			}
			if hash := enrollHash(t, store, fx, id); hash != legacy {
				t.Fatalf("wrong-target attempt upgraded the %s row: %q -> %q", fx.name, legacy, hash)
			}

			if _, err := fx.verify(store, plain, "demo", "gw-demo"); err != nil {
				t.Fatalf("bound %s token rejected on its own target: %v", fx.name, err)
			}
			if hash := enrollHash(t, store, fx, id); hash != tokenFastHash(plain) {
				t.Fatalf("bound %s token did not upgrade on its own target: %q", fx.name, hash)
			}
		})
	}
}

// 8: neither enrollment table stores an expiry, and neither Verify function
// reads created_at, so an aged row stays valid. This pins the absence of a TTL
// (adding one would be a new decision, not an INT-69 change).
func TestEnrollTokenHashAgedRowStillVerifies(t *testing.T) {
	for _, fx := range enrollFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			store := openTokenHashStore(t)
			id, plain := fx.issue(t, store, "demo", "gw-demo")
			var stmt string
			switch fx.name {
			case "agent":
				stmt = `UPDATE agent_enrollments SET created_at = ? WHERE id = ?`
			case "edge":
				stmt = `UPDATE edge_enrollments SET created_at = ? WHERE id = ?`
			default:
				t.Fatalf("unknown enrollment fixture %q", fx.name)
			}
			if _, err := store.db.Exec(stmt, "2000-01-01T00:00:00Z", id); err != nil {
				t.Fatalf("age %s enrollment: %v", fx.name, err)
			}
			if _, err := fx.verify(store, plain, "demo", "gw-demo"); err != nil {
				t.Fatalf("aged %s enrollment rejected: %v", fx.name, err)
			}
		})
	}
}

// 9: confirming an agent enrollment changes enforcement, not the token. The
// token stays reusable until it is revoked; INT-69 adds no one-time-use check.
func TestEnrollTokenHashConfirmedAgentEnrollmentStillVerifies(t *testing.T) {
	fx := enrollFixtures(t)[0]
	store := openTokenHashStore(t)
	id, plain := fx.issue(t, store, "demo", "gw-demo")
	if err := fx.confirm(store, id); err != nil {
		t.Fatalf("confirm enrollment: %v", err)
	}
	if _, err := fx.verify(store, plain, "demo", "gw-demo"); err != nil {
		t.Fatalf("confirmed agent enrollment rejected: %v", err)
	}
}

// 10: eight goroutines race the first use of one legacy row. The trigger counts
// real rewrites, so this asserts the database saw exactly one upgrade.
func TestEnrollTokenHashConcurrentLegacyUseUpgradesOnce(t *testing.T) {
	for _, fx := range enrollFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			store := openTokenHashStore(t)
			id, plain := fx.issue(t, store, "demo", "gw-demo")
			presetLegacyEnrollHash(t, store, fx, id, plain)
			installEnrollWriteCounter(t, store, fx)

			const goroutines = 8
			start := make(chan struct{})
			errs := make(chan error, goroutines)
			var wg sync.WaitGroup
			for i := 0; i < goroutines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					gotID, err := fx.verify(store, plain, "demo", "gw-demo")
					if err != nil {
						errs <- err
						return
					}
					if gotID != id {
						errs <- fmt.Errorf("verified id %s, want %s", gotID, id)
					}
				}()
			}
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatalf("concurrent %s verification failed: %v", fx.name, err)
			}

			if n := enrollHashWriteCount(t, store); n != 1 {
				t.Fatalf("concurrent first use rewrote the %s hash %d times, want exactly 1", fx.name, n)
			}
			if hash := enrollHash(t, store, fx, id); hash != tokenFastHash(plain) {
				t.Fatalf("after concurrent use the %s hash = %q, want the fast form of the plaintext", fx.name, hash)
			}
		})
	}
}
