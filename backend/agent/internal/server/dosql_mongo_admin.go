package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const mongoDocumentTextLimit = 64 * 1024

// mongoBoundedText keeps one oversized admin document (serverStatus, collStats)
// from flooding the result and the evidence file.
func mongoBoundedText(doc interface{}) string {
	text := mongoDocumentText(doc)
	if len(text) > mongoDocumentTextLimit {
		return text[:mongoDocumentTextLimit] + "...[truncated]"
	}
	return text
}

func mongoRunCommand(ctx context.Context, db *mongo.Database, command bson.D) (bson.D, error) {
	var out bson.D
	if err := db.RunCommand(ctx, command).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func mongoSingleDocument(executed doSQLStatementResult, doc interface{}) doSQLStatementResult {
	executed.Columns = []string{"document"}
	executed.Rows = [][]string{{mongoBoundedText(doc)}}
	return executed
}

// runDoSQLMongoExtended implements the operations beyond plain CRUD: discovery,
// statistics, plans, atomic find-and-modify, collection and database
// administration. Anything that needs a password (user management) is
// deliberately absent: it would land in the statement, journal and evidence.
func runDoSQLMongoExtended(ctx context.Context, database *mongo.Database, op mongoOperation, executed doSQLStatementResult) (doSQLStatementResult, error) {
	client := database.Client()
	admin := client.Database("admin")
	collection := database.Collection(op.Collection)
	switch op.Op {
	case "listDatabases":
		listed, err := client.ListDatabases(ctx, bson.D{})
		if err != nil {
			return executed, err
		}
		executed.Columns = []string{"name", "size_on_disk", "empty"}
		for _, item := range listed.Databases {
			executed.Rows = append(executed.Rows, []string{item.Name, fmt.Sprint(item.SizeOnDisk), fmt.Sprint(item.Empty)})
		}
	case "listCollections":
		specs, err := database.ListCollectionSpecifications(ctx, bson.D{})
		if err != nil {
			return executed, err
		}
		executed.Columns = []string{"name", "type", "read_only"}
		for _, spec := range specs {
			executed.Rows = append(executed.Rows, []string{spec.Name, spec.Type, fmt.Sprint(spec.ReadOnly)})
		}
	case "listIndexes":
		specs, err := collection.Indexes().ListSpecifications(ctx)
		if err != nil {
			return executed, err
		}
		executed.Columns = []string{"name", "keys", "unique", "expire_after_seconds"}
		for _, spec := range specs {
			unique, expire := "false", ""
			if spec.Unique != nil && *spec.Unique {
				unique = "true"
			}
			if spec.ExpireAfterSeconds != nil {
				expire = fmt.Sprint(*spec.ExpireAfterSeconds)
			}
			executed.Rows = append(executed.Rows, []string{spec.Name, mongoDocumentText(spec.KeysDocument), unique, expire})
		}
	case "dbStats":
		out, err := mongoRunCommand(ctx, database, bson.D{{Key: "dbStats", Value: 1}})
		if err != nil {
			return executed, err
		}
		return mongoSingleDocument(executed, out), nil
	case "collStats":
		out, err := mongoRunCommand(ctx, database, bson.D{{Key: "collStats", Value: op.Collection}})
		if err != nil {
			return executed, err
		}
		return mongoSingleDocument(executed, out), nil
	case "serverStatus", "buildInfo", "replSetStatus":
		name := map[string]string{"serverStatus": "serverStatus", "buildInfo": "buildInfo", "replSetStatus": "replSetGetStatus"}[op.Op]
		out, err := mongoRunCommand(ctx, admin, bson.D{{Key: name, Value: 1}})
		if err != nil {
			return executed, err
		}
		return mongoSingleDocument(executed, out), nil
	case "currentOp":
		// Only identifying fields: the command text of other clients can carry
		// credentials or personal data and is not needed to find a stuck operation.
		var reply struct {
			Inprog []bson.M `bson:"inprog"`
		}
		if err := admin.RunCommand(ctx, bson.D{{Key: "currentOp", Value: 1}}).Decode(&reply); err != nil {
			return executed, err
		}
		executed.Columns = []string{"opid", "op", "ns", "secs_running", "active", "client", "desc"}
		for _, item := range reply.Inprog {
			if len(executed.Rows) == doSQLDirectRowLimit {
				executed.Truncated = true
				break
			}
			row := make([]string, 0, len(executed.Columns))
			for _, column := range executed.Columns {
				row = append(row, redactSecretText(fmt.Sprint(item[column])))
			}
			executed.Rows = append(executed.Rows, row)
		}
	case "killOp":
		if _, err := mongoRunCommand(ctx, admin, bson.D{{Key: "killOp", Value: 1}, {Key: "op", Value: *op.OpID}}); err != nil {
			return executed, err
		}
	case "distinct":
		filter, err := mongoBSON(op.Filter)
		if err != nil {
			return executed, err
		}
		var values bson.A
		if err := collection.Distinct(ctx, op.Field, filter).Decode(&values); err != nil {
			return executed, err
		}
		executed.Columns = []string{"value"}
		for _, value := range values {
			if len(executed.Rows) == doSQLDirectRowLimit {
				executed.Truncated = true
				break
			}
			executed.Rows = append(executed.Rows, []string{mongoDocumentText(bson.D{{Key: "v", Value: value}})})
		}
	case "explain":
		filter, err := mongoBSON(op.Filter)
		if err != nil {
			return executed, err
		}
		find := bson.D{{Key: "find", Value: op.Collection}, {Key: "filter", Value: filter}}
		if len(op.Sort) != 0 {
			sort, err := mongoBSON(op.Sort)
			if err != nil {
				return executed, err
			}
			find = append(find, bson.E{Key: "sort", Value: sort})
		}
		verbosity := op.Verbosity
		if verbosity == "" {
			verbosity = "queryPlanner"
		}
		out, err := mongoRunCommand(ctx, database, bson.D{{Key: "explain", Value: find}, {Key: "verbosity", Value: verbosity}})
		if err != nil {
			return executed, err
		}
		return mongoSingleDocument(executed, out), nil
	case "findOneAndUpdate", "findOneAndReplace", "findOneAndDelete":
		filter, err := mongoBSON(op.Filter)
		if err != nil {
			return executed, err
		}
		after := op.Return == "after"
		returnDocument := options.Before
		if after {
			returnDocument = options.After
		}
		var reply *mongo.SingleResult
		switch op.Op {
		case "findOneAndUpdate":
			var update interface{}
			if strings.HasPrefix(strings.TrimSpace(string(op.Update)), "[") {
				stages, err := mongoBSONList(op.Update)
				if err != nil {
					return executed, err
				}
				update = stages
			} else if update, err = mongoBSON(op.Update); err != nil {
				return executed, err
			}
			reply = collection.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetUpsert(op.Upsert).SetReturnDocument(returnDocument))
		case "findOneAndReplace":
			replacement, err := mongoBSON(op.Replacement)
			if err != nil {
				return executed, err
			}
			reply = collection.FindOneAndReplace(ctx, filter, replacement, options.FindOneAndReplace().SetUpsert(op.Upsert).SetReturnDocument(returnDocument))
		default:
			reply = collection.FindOneAndDelete(ctx, filter)
		}
		var document bson.D
		switch err := reply.Decode(&document); {
		case errors.Is(err, mongo.ErrNoDocuments):
			return executed, nil
		case err != nil:
			return executed, err
		}
		executed.AffectedRows = 1
		executed.Columns = []string{"document"}
		executed.Rows = [][]string{{mongoBoundedText(document)}}
	case "renameCollection":
		from := database.Name() + "." + op.Collection
		to := database.Name() + "." + op.To
		if _, err := mongoRunCommand(ctx, admin, bson.D{{Key: "renameCollection", Value: from}, {Key: "to", Value: to}}); err != nil {
			return executed, err
		}
	case "collMod":
		options, err := mongoBSON(op.Options)
		if err != nil {
			return executed, err
		}
		command := append(bson.D{{Key: "collMod", Value: op.Collection}}, options...)
		if _, err := mongoRunCommand(ctx, database, command); err != nil {
			return executed, err
		}
	case "compact":
		if _, err := mongoRunCommand(ctx, database, bson.D{{Key: "compact", Value: op.Collection}}); err != nil {
			return executed, err
		}
	case "dropDatabase":
		if err := database.Drop(ctx); err != nil {
			return executed, err
		}
	default:
		return executed, fmt.Errorf("dosql_mongo_operation_invalid: op %q has no executor", op.Op)
	}
	return executed, nil
}
