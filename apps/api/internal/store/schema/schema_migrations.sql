-- golang-migrate creates and owns this table when `api migrate up` first
-- runs (its pgx v5 driver's DDL, below); no migration does. This file only
-- tells sqlc its shape, so a query can read the schema version. It is never
-- applied.
create table schema_migrations (
  version bigint not null primary key,
  dirty boolean not null
);
