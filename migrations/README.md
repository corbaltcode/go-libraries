# migrations

This library is for managing schema migrations in Postgres. You give it an array of `NamedMigration`s and it connects to the configured database and:
1. Verifies that the `migration` table exists, and creates it if it does not.
2. Verifies that the existing migrations recorded in the database match (by name and order) the migrations given in the argument.
3. Performs any migrations that are not yet recorded in the database.

## Writing migrations

You should have your list of migrations committed to a file as one big slice. To add a migration, add a new entry to the end of the slice. In general, a migration is simply a function that accepts a `*sqlx.Tx` (transaction context) and returns an error. It is expected to execute whatever SQL commands are necessary to effect the migration.

Things to note when writing migrations:
- Do all database access using the transaction passed in to the function. Do not commit or roll back manually; the migration harness will do this automatically as appropriate. Simply return nil if the migration succeeded or an error if it did not.
- Each migration should be self-contained. Do not reference any external functions which load rows into structs or otherwise assume anything about the structure of the database, since those functions may change as migrations happen.
- If your migration can be expressed in pure SQL, use the `StaticMigration` type.
- Do not edit previously merged migrations or their name or order.