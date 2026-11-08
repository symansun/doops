# MongoDB Rules

MongoDB is supported as a database change target even though changes are not SQL.
DoSql still treats commands as database operations and records them through the
same proxy.

## Read Operations

Read-only commands include:

- `find`;
- `findOne`;
- `aggregate`;
- `countDocuments`;
- `distinct`;
- `explain`.

## Change Operations

MongoDB changes enter the change document:

- `insertOne`, `insertMany`;
- `updateOne`, `updateMany`;
- `replaceOne`;
- `deleteOne`, `deleteMany`;
- `bulkWrite`;
- `createIndex`, `dropIndex`;
- `createCollection`, `drop`, `renameCollection`;
- user and role operations.

## Migration Script Form

The Skill may render either:

- JavaScript migration script for `mongosh`;
- structured JSON command document for a controlled runner.

The response must include the script fingerprint and verification output.
