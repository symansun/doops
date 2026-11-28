package recorder

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

func TestStoreAppendAndFilter(t *testing.T) {
	store := NewStore(t.TempDir(), "mysql", "db.internal:3306", "orders")
	err := store.Append(
		StatementRecord{SessionID: "dev-1", OperationID: "dbop_1", Engine: "mysql", Database: "orders", Mode: "query", Kind: "read", Statement: "SELECT 1", Status: "succeeded"},
		StatementRecord{SessionID: "dev-1", OperationID: "dbop_2", Engine: "mysql", Database: "orders", Mode: "apply", Kind: "ddl", Statement: "ALTER TABLE orders ADD COLUMN v2 INT", Status: "succeeded", ReleaseTag: "v1.2.0"},
		StatementRecord{SessionID: "dev-2", OperationID: "dbop_3", Engine: "mysql", Database: "orders", Mode: "apply", Kind: "dml", Statement: "UPDATE orders SET v2 = 1 WHERE id = 1", Status: "failed", Error: "lock timeout"},
	)
	if err != nil {
		t.Fatal(err)
	}
	all, err := store.List(QueryFilter{})
	if err != nil || len(all) != 3 || all[2].Seq != 3 {
		t.Fatalf("unexpected all: %+v err=%v", all, err)
	}
	muts, err := store.List(QueryFilter{SessionID: "dev-1", OnlyMutations: true, OnlySucceeded: true})
	if err != nil || len(muts) != 1 || muts[0].ReleaseTag != "v1.2.0" {
		t.Fatalf("unexpected filtered mutations: %+v err=%v", muts, err)
	}
}

func TestStoreCanonicalAddressConcurrencyAndLimit(t *testing.T) {
	root := t.TempDir()
	sA := NewStore(root, "MYSQL", "DB-B.TEST:3306,db-a.test:3306", "orders")
	sB := NewStore(root, "mysql", "db-a.test:3306,db-b.test:3306", "orders")
	if sA.Path != sB.Path {
		t.Fatalf("canonical history paths differ: %q vs %q", sA.Path, sB.Path)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if err := sA.Append(StatementRecord{
				SessionID: "s1", OperationID: fmt.Sprintf("op_%d", idx),
				Engine: "mysql", Database: "orders", Mode: "apply", Kind: "dml",
				Statement: fmt.Sprintf("UPDATE t SET a = %d WHERE id = %d", idx, idx), Status: "succeeded",
			}); err != nil {
				t.Errorf("concurrent append %d: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	// Append one corrupt line to ensure List skips corrupt lines safely.
	f, err := os.OpenFile(sA.Path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{corrupt json\n")
	_ = f.Close()

	all, err := sB.List(QueryFilter{})
	if err != nil || len(all) != 8 {
		t.Fatalf("expected 8 concurrent records, got %d err=%v", len(all), err)
	}
	tail, err := sB.List(QueryFilter{Limit: 3})
	if err != nil || len(tail) != 3 || tail[2].Seq != 8 {
		t.Fatalf("Limit tail mismatch: %+v err=%v", tail, err)
	}
}
