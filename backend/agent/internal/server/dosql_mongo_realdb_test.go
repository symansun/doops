package server

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Real MongoDB acceptance. Transactions need a replica set, so this runs only
// against a throwaway one:
//
//	DOOPS_TEST_MONGO_DSN='mongodb://127.0.0.1:27019/testdb?directConnection=true'
func realMongoTarget(t *testing.T) doSQLTarget {
	t.Helper()
	dsn := os.Getenv("DOOPS_TEST_MONGO_DSN")
	if dsn == "" {
		t.Skip("DOOPS_TEST_MONGO_DSN not set")
	}
	return doSQLTarget{Engine: "mongodb", Driver: "mongo", DSN: dsn, Database: "testdb"}
}

func realMongoRun(t *testing.T, target doSQLTarget, apply, dryRun bool, verify string, statements ...string) (doSQLDirectResult, error) {
	t.Helper()
	return executeDoSQLDirect(context.Background(), doSQLDirectRequest{
		Target: target, Statements: statements, Verification: verify, ReadOnly: !apply, DryRun: dryRun,
	})
}

func realMongoCount(t *testing.T, target doSQLTarget, collection, filter string) string {
	t.Helper()
	result, err := realMongoRun(t, target, false, false, "",
		`{"collection":"`+collection+`","op":"count","filter":`+filter+`}`)
	if err != nil {
		t.Fatalf("count %s: %v", collection, err)
	}
	return result.Statements[0].Rows[0][0]
}

func TestRealDoSQLMongoTransactionsAndBoundaries(t *testing.T) {
	target := realMongoTarget(t)
	ctx := context.Background()
	_ = ctx
	for _, name := range []string{"zz_m_a", "zz_m_b", "zz_m_c"} {
		realMongoRun(t, target, true, false, "", `{"collection":"`+name+`","op":"drop"}`)
	}
	t.Cleanup(func() {
		for _, name := range []string{"zz_m_a", "zz_m_b", "zz_m_c"} {
			realMongoRun(t, target, true, false, "", `{"collection":"`+name+`","op":"drop"}`)
		}
	})

	t.Run("collection DDL applies and is reported as non transactional", func(t *testing.T) {
		result, err := realMongoRun(t, target, true, false, "",
			`{"collection":"zz_m_a","op":"createCollection"}`,
			`{"collection":"zz_m_a","op":"createIndex","keys":{"sku":1},"unique":true,"name":"sku_unique"}`)
		if err != nil || !result.Committed || result.ImplicitCommits != 2 {
			t.Fatalf("ddl: %#v %v", result, err)
		}
	})
	t.Run("committed writes persist", func(t *testing.T) {
		result, err := realMongoRun(t, target, true, false,
			`{"collection":"zz_m_a","op":"count","filter":{}}`,
			`{"collection":"zz_m_a","op":"insertMany","documents":[{"sku":"a","qty":1},{"sku":"b","qty":2}]}`,
			`{"collection":"zz_m_a","op":"updateOne","filter":{"sku":"a"},"update":{"$set":{"qty":10}}}`)
		if err != nil || !result.Committed || result.Status != "verified" || result.Verification.Rows[0][0] != "2" {
			t.Fatalf("writes: %#v %v", result, err)
		}
		if got := realMongoCount(t, target, "zz_m_a", `{"qty":10}`); got != "1" {
			t.Fatalf("update lost: %s", got)
		}
	})
	t.Run("failed batch rolls back every earlier write", func(t *testing.T) {
		_, err := realMongoRun(t, target, true, false, "",
			`{"collection":"zz_m_a","op":"updateOne","filter":{"sku":"b"},"update":{"$set":{"qty":99}}}`,
			`{"collection":"zz_m_a","op":"insertOne","document":{"sku":"a","qty":5}}`)
		if err == nil || !strings.Contains(err.Error(), "dosql_statement_failed: statement 2") {
			t.Fatalf("duplicate key accepted: %v", err)
		}
		if got := realMongoCount(t, target, "zz_m_a", `{"qty":99}`); got != "0" {
			t.Fatalf("first write survived the failed batch: %s", got)
		}
	})
	t.Run("failed verification rolls the writes back", func(t *testing.T) {
		_, err := realMongoRun(t, target, true, false, `{"collection":"zz_m_a","op":"aggregate","pipeline":[{"$bogus":1}]}`,
			`{"collection":"zz_m_a","op":"deleteMany","filter":{}}`)
		if err == nil || !strings.Contains(err.Error(), "dosql_verification_failed") {
			t.Fatalf("expected verification failure, got %v", err)
		}
		if got := realMongoCount(t, target, "zz_m_a", `{}`); got != "2" {
			t.Fatalf("delete survived a failed verification: %s", got)
		}
	})
	t.Run("dry run reports the effect and changes nothing", func(t *testing.T) {
		result, err := realMongoRun(t, target, true, true, `{"collection":"zz_m_a","op":"count","filter":{}}`,
			`{"collection":"zz_m_a","op":"deleteMany","filter":{}}`)
		if err != nil || result.Status != "dry_run_rolled_back" || result.Committed || result.Verification.Rows[0][0] != "0" {
			t.Fatalf("dry run: %#v %v", result, err)
		}
		if got := realMongoCount(t, target, "zz_m_a", `{}`); got != "2" {
			t.Fatalf("dry run deleted data: %s", got)
		}
	})
	t.Run("dry run with collection DDL is refused before connecting", func(t *testing.T) {
		_, err := realMongoRun(t, target, true, true, "", `{"collection":"zz_m_b","op":"createCollection"}`)
		if err == nil || !strings.Contains(err.Error(), "dosql_dry_run_ddl_unsupported") {
			t.Fatalf("expected refusal, got %v", err)
		}
		names := realMongoRunNames(t, target)
		if strings.Contains(names, "zz_m_b") {
			t.Fatal("rejected dry run still created the collection")
		}
	})
	t.Run("collection DDL mixed with writes is refused", func(t *testing.T) {
		_, err := realMongoRun(t, target, true, false, "",
			`{"collection":"zz_m_c","op":"createCollection"}`,
			`{"collection":"zz_m_a","op":"insertOne","document":{"sku":"z"}}`)
		if err == nil || !strings.Contains(err.Error(), "dosql_mixed_ddl_dml_unsupported") {
			t.Fatalf("expected refusal, got %v", err)
		}
		if got := realMongoCount(t, target, "zz_m_a", `{"sku":"z"}`); got != "0" {
			t.Fatalf("write ran despite the refusal: %s", got)
		}
	})
	t.Run("query mode refuses writes and DDL", func(t *testing.T) {
		for _, statement := range []string{
			`{"collection":"zz_m_a","op":"deleteMany","filter":{}}`,
			`{"collection":"zz_m_a","op":"drop"}`,
		} {
			if _, err := realMongoRun(t, target, false, false, "", statement); err == nil {
				t.Fatalf("query mode ran %s", statement)
			}
		}
		if got := realMongoCount(t, target, "zz_m_a", `{}`); got != "2" {
			t.Fatalf("query mode changed data: %s", got)
		}
	})
	t.Run("server side JavaScript and cross-transaction writes are refused", func(t *testing.T) {
		for _, statement := range []string{
			`{"collection":"zz_m_a","op":"find","filter":{"$where":"sleep(1)"}}`,
			`{"collection":"zz_m_a","op":"aggregate","pipeline":[{"$out":"zz_m_c"}]}`,
			`{"collection":"system.users","op":"find"}`,
			`{"collection":"zz_m_a","op":"deleteMany"}`,
			`{"collection":"zz_m_a","op":"unknown"}`,
		} {
			if _, err := parseMongoStatements(statement); err == nil {
				t.Fatalf("accepted %s", statement)
			}
		}
	})
	t.Run("find is bounded", func(t *testing.T) {
		docs := make([]string, 0, 250)
		for i := 0; i < 250; i++ {
			docs = append(docs, `{"n":`+strings.Repeat("1", 1)+`}`)
		}
		if _, err := realMongoRun(t, target, true, false, "",
			`{"collection":"zz_m_c","op":"insertMany","documents":[`+strings.Join(docs, ",")+`]}`); err != nil {
			t.Fatal(err)
		}
		result, err := realMongoRun(t, target, false, false, "", `{"collection":"zz_m_c","op":"find","filter":{"n":1}}`)
		if err != nil || !result.Statements[0].Truncated || len(result.Statements[0].Rows) != doSQLDirectRowLimit {
			t.Fatalf("rows not bounded: %#v %v", result.Statements, err)
		}
	})
}

func realMongoRunNames(t *testing.T, target doSQLTarget) string {
	t.Helper()
	client, _, err := connectDoSQLMongo(context.Background(), target.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	names, err := client.Database(target.Database).ListCollectionNames(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(names, ",")
}
