package server

import (
	"strings"
	"testing"
)

func TestParseMongoStatementsCanonicalizesAndValidates(t *testing.T) {
	got, err := parseMongoStatements(`[ {"collection":"orders", "op":"find", "filter":{ "a":1 }} ,
		{"collection":"orders","op":"deleteMany","filter":{}} ]`)
	if err != nil || len(got) != 2 || strings.Contains(got[0], " ") {
		t.Fatalf("canonical statements = %#v, %v", got, err)
	}
	// consecutive documents without an array are accepted too
	if got, err := parseMongoStatements(`{"collection":"a","op":"count","filter":{}} {"collection":"b","op":"count","filter":{}}`); err != nil || len(got) != 2 {
		t.Fatalf("consecutive documents = %#v, %v", got, err)
	}
	for name, statement := range map[string]string{
		"javascript where":    `{"collection":"a","op":"find","filter":{"$where":"1"}}`,
		"javascript function": `{"collection":"a","op":"aggregate","pipeline":[{"$addFields":{"x":{"$function":{}}}}]}`,
		"out stage":           `{"collection":"a","op":"aggregate","pipeline":[{"$out":"b"}]}`,
		"merge stage":         `{"collection":"a","op":"aggregate","pipeline":[{"$merge":{"into":"b"}}]}`,
		"system collection":   `{"collection":"system.users","op":"find"}`,
		"missing filter":      `{"collection":"a","op":"deleteMany"}`,
		"update no filter":    `{"collection":"a","op":"updateMany","update":{"$set":{"x":1}}}`,
		"unknown op":          `{"collection":"a","op":"eval"}`,
		"unknown field":       `{"collection":"a","op":"find","bogus":1}`,
		"drop all indexes":    `{"collection":"a","op":"dropIndex","name":"*"}`,
		"empty":               ``,
		"bad json":            `{"collection":`,
	} {
		if _, err := parseMongoStatements(statement); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestMongoPlanningUsesEngineKinds(t *testing.T) {
	if !directReadOnlyStatement("mongodb", `{"collection":"a","op":"find"}`) ||
		directReadOnlyStatement("mongodb", `{"collection":"a","op":"insertOne","document":{}}`) {
		t.Fatal("mongodb read-only classification is wrong")
	}
}

func TestInferDoSQLEngineAcceptsMongoDB(t *testing.T) {
	engine, database, err := inferDoSQLEngine(map[string]string{"connection_uri": "mongodb://u:p@db:27017/zyj"})
	if err != nil || engine != "mongodb" || database != "zyj" {
		t.Fatalf("infer = %s %s %v", engine, database, err)
	}
}
