-- name: SchemaVersion :one
-- The schema version golang-migrate recorded, and whether the migration to
-- it failed partway. golang-migrate keeps exactly one row.
select version, dirty
from schema_migrations
limit 1;
