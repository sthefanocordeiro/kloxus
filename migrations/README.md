# Migrations

Contract enforced by the runner (`internal/store/db.go`, `migrate()`), checked
on every boot:

- Files are named `NNNN_descricao.sql` and apply in lexical order.
- **`NNNN` never repeats.** Boot fails on a duplicate or malformed name —
  renumbering to a unique sequence happened in KLOX-222 after several numbers
  collided (`4×0002`, `2×0005`, `3×0008`, `2×0009`, `2×0011`, `2×0013`,
  `2×0018`).
- **Never edit a file that may already be applied.** `schema_migrations`
  stores a sha256 checksum of the file bytes; a mismatch fails the boot.
  Rows recorded before checksums existed adopt the current hash once, on the
  first migrate after upgrade — that adoption window was closed by KLOX-222's
  deploy.
- **Never rename a file without a `migrationRenames` entry** in
  `internal/store/db.go`. `schema_migrations.version` stores the file name,
  so an unmapped rename re-applies the file on already-migrated databases.
  Map entries are permanent: a database that skipped releases still needs the
  rename to converge.
- New files take the next free `NNNN`. Gaps are fine; repetition is not.
- Each file runs in its own transaction. `CREATE ... IF NOT EXISTS` keeps a
  file idempotent, which is what let zero-statement consolidation files (like
  `0020_item_summaries.sql`) stay as recorded no-ops.
