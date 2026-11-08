package server

import (
	"strings"
	"testing"
)

func TestRealDoSQLMongoOperations(t *testing.T) {
	target := realMongoTarget(t)
	cleanup := func() {
		for _, name := range []string{"zz_o_a", "zz_o_b", "zz_o_c"} {
			realMongoRun(t, target, true, false, "", `{"collection":"`+name+`","op":"drop"}`)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	seed := func(t *testing.T) {
		t.Helper()
		cleanup()
		if _, err := realMongoRun(t, target, true, false, "",
			`{"collection":"zz_o_a","op":"insertMany","documents":[{"sku":"a","qty":1,"tag":"x"},{"sku":"b","qty":2,"tag":"x"},{"sku":"c","qty":3,"tag":"y"}]}`); err != nil {
			t.Fatal(err)
		}
	}
	rows := func(t *testing.T, apply bool, statement string) [][]string {
		t.Helper()
		result, err := realMongoRun(t, target, apply, false, "", statement)
		if err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
		return result.Statements[0].Rows
	}

	t.Run("discovery reads run in query mode", func(t *testing.T) {
		seed(t)
		if got := rows(t, false, `{"op":"listDatabases"}`); len(got) == 0 {
			t.Fatal("no databases listed")
		}
		found := false
		for _, row := range rows(t, false, `{"op":"listCollections"}`) {
			found = found || row[0] == "zz_o_a"
		}
		if !found {
			t.Fatal("listCollections misses zz_o_a")
		}
		if got := rows(t, false, `{"collection":"zz_o_a","op":"listIndexes"}`); len(got) == 0 || got[0][0] != "_id_" {
			t.Fatalf("indexes: %v", got)
		}
		if got := rows(t, false, `{"collection":"zz_o_a","op":"distinct","field":"tag"}`); len(got) != 2 {
			t.Fatalf("distinct: %v", got)
		}
		for _, op := range []string{"dbStats", "serverStatus", "buildInfo", "replSetStatus", "currentOp"} {
			if got := rows(t, false, `{"op":"`+op+`"}`); len(got) == 0 && op != "currentOp" {
				t.Fatalf("%s returned nothing", op)
			}
		}
		if got := rows(t, false, `{"collection":"zz_o_a","op":"collStats"}`); len(got) == 0 {
			t.Fatal("collStats returned nothing")
		}
	})

	t.Run("explain reports the plan without running the query", func(t *testing.T) {
		seed(t)
		got := rows(t, false, `{"collection":"zz_o_a","op":"explain","filter":{"sku":"a"},"verbosity":"executionStats"}`)
		if len(got) != 1 || !strings.Contains(got[0][0], "executionStats") {
			t.Fatalf("explain: %v", got)
		}
	})

	t.Run("findOneAndUpdate is atomic, returns the chosen side and rolls back with the batch", func(t *testing.T) {
		seed(t)
		got := rows(t, true, `{"collection":"zz_o_a","op":"findOneAndUpdate","filter":{"sku":"a"},"update":{"$set":{"qty":10}},"return":"before"}`)
		if len(got) != 1 || !strings.Contains(got[0][0], `"qty":1`) {
			t.Fatalf("before image: %v", got)
		}
		got = rows(t, true, `{"collection":"zz_o_a","op":"findOneAndUpdate","filter":{"sku":"a"},"update":{"$set":{"qty":11}},"return":"after"}`)
		if len(got) != 1 || !strings.Contains(got[0][0], `"qty":11`) {
			t.Fatalf("after image: %v", got)
		}
		_, err := realMongoRun(t, target, true, false, "",
			`{"collection":"zz_o_a","op":"findOneAndDelete","filter":{"sku":"b"}}`,
			`{"collection":"zz_o_a","op":"insertOne","document":{"_id":{"$oid":"000000000000000000000001"}}}`,
			`{"collection":"zz_o_a","op":"insertOne","document":{"_id":{"$oid":"000000000000000000000001"}}}`)
		if err == nil {
			t.Fatal("duplicate _id accepted")
		}
		if got := realMongoCount(t, target, "zz_o_a", `{"sku":"b"}`); got != "1" {
			t.Fatalf("findOneAndDelete survived a failed batch: %s", got)
		}
		if got := rows(t, true, `{"collection":"zz_o_a","op":"findOneAndDelete","filter":{"sku":"zzz"}}`); len(got) != 0 {
			t.Fatalf("no match must return nothing: %v", got)
		}
	})

	t.Run("dry run of findOneAndUpdate changes nothing", func(t *testing.T) {
		seed(t)
		result, err := realMongoRun(t, target, true, true, `{"collection":"zz_o_a","op":"count","filter":{"qty":99}}`,
			`{"collection":"zz_o_a","op":"findOneAndUpdate","filter":{"sku":"a"},"update":{"$set":{"qty":99}}}`)
		if err != nil || result.Status != "dry_run_rolled_back" || result.Verification.Rows[0][0] != "1" {
			t.Fatalf("dry run: %#v %v", result, err)
		}
		if got := realMongoCount(t, target, "zz_o_a", `{"qty":99}`); got != "0" {
			t.Fatalf("dry run changed data: %s", got)
		}
	})

	t.Run("index options, rename, collMod and compact are reported as non transactional", func(t *testing.T) {
		seed(t)
		result, err := realMongoRun(t, target, true, false, "",
			`{"collection":"zz_o_a","op":"createIndex","keys":{"tag":1},"name":"tag_partial","partialFilter":{"qty":{"$gt":0}}}`,
			`{"collection":"zz_o_a","op":"renameCollection","to":"zz_o_b"}`)
		if err != nil || result.ImplicitCommits != 2 || !result.Committed || result.Status != "verified" {
			t.Fatalf("admin batch: %#v %v", result, err)
		}
		if got := rows(t, false, `{"collection":"zz_o_b","op":"listIndexes"}`); len(got) != 2 {
			t.Fatalf("index did not follow the rename: %v", got)
		}
		if _, err := realMongoRun(t, target, true, false, "",
			`{"collection":"zz_o_b","op":"collMod","options":{"validator":{"qty":{"$type":"number"}},"validationLevel":"moderate"}}`); err != nil {
			t.Fatal(err)
		}
		// The server refuses compact on a primary; that refusal must reach the caller
		// instead of being forced or swallowed.
		if _, err := realMongoRun(t, target, true, false, "", `{"collection":"zz_o_b","op":"compact"}`); err == nil ||
			!strings.Contains(err.Error(), "IllegalOperation") {
			t.Fatalf("compact on a primary must surface the server refusal, got %v", err)
		}
	})

	t.Run("killOp reaches the server and an unknown opid is not an error", func(t *testing.T) {
		if _, err := realMongoRun(t, target, true, false, "", `{"op":"killOp","opid":123456789}`); err != nil {
			t.Fatalf("killOp: %v", err)
		}
	})

	t.Run("dropDatabase needs the matching confirmation", func(t *testing.T) {
		if _, err := realMongoRun(t, target, true, false, "", `{"op":"dropDatabase","confirmDatabase":"other"}`); err == nil ||
			!strings.Contains(err.Error(), "confirmDatabase") {
			t.Fatalf("a mismatching confirmation must be refused, got %v", err)
		}
		if _, err := realMongoRun(t, target, true, true, "", `{"op":"dropDatabase","confirmDatabase":"testdb"}`); err == nil ||
			!strings.Contains(err.Error(), "dosql_dry_run_ddl_unsupported") {
			t.Fatalf("a dry run must not drop a database, got %v", err)
		}
		seed(t)
		if got := realMongoCount(t, target, "zz_o_a", `{}`); got != "3" {
			t.Fatalf("data lost by refused drops: %s", got)
		}
	})

	t.Run("admin operations are refused in query mode and secrets stay out of output", func(t *testing.T) {
		for _, statement := range []string{
			`{"op":"killOp","opid":1}`,
			`{"collection":"zz_o_a","op":"renameCollection","to":"zz_o_c"}`,
			`{"collection":"zz_o_a","op":"findOneAndDelete","filter":{}}`,
			`{"op":"dropDatabase","confirmDatabase":"testdb"}`,
		} {
			if _, err := realMongoRun(t, target, false, false, "", statement); err == nil {
				t.Fatalf("query mode ran %s", statement)
			}
		}
		for _, statement := range []string{
			`{"op":"createUser","user":"x","pwd":"y"}`,
			`{"collection":"zz_o_a","op":"find","filter":{"$function":{}}}`,
		} {
			if _, err := parseMongoStatements(statement); err == nil {
				t.Fatalf("accepted %s", statement)
			}
		}
	})
}
