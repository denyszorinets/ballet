Persistence
===========

Every service that stores data owns one SQLite database file, used
through ``kit/sqlstore``
(:doc:`/architecture/decisions/0019-sqlite-first-rqlite-later`,
:doc:`/architecture/decisions/0016-durable-orchestration-in-the-database`).
The driver is modernc.org/sqlite (pure Go, no cgo).

Rules
-----

- **Reads** use ``DB.Query`` / ``DB.QueryRow``.
- **Writes** use ``DB.Batch`` only. A batch is a list of statements
  executed atomically, with no Go code running between them. Never open
  an interactive transaction: rqlite, the planned high-availability
  backend, cannot execute them.
- **Optimistic concurrency**: every mutable row has a ``version``
  column. Updates are guarded with ``sqlstore.ExecOne``:

  .. code-block:: go

     err := db.Batch(ctx,
         sqlstore.ExecOne(
             `UPDATE tickets SET state = ?, version = version + 1
              WHERE id = ? AND version = ?`, next, id, expectedVersion),
         sqlstore.Exec(
             `INSERT INTO events (entity_id, kind) VALUES (?, ?)`, id, "state_changed"),
     )
     if errors.Is(err, sqlstore.ErrConflict) {
         // someone else changed the ticket: re-read and decide again
     }

  If the guarded statement does not change exactly one row, the whole
  batch is rolled back and ``ErrConflict`` is returned.

How the guard works
~~~~~~~~~~~~~~~~~~~

After each ``ExecOne`` statement the batch runs
``INSERT OR REPLACE INTO ballet_guard (id, ok) VALUES (1, changes() = 1)``.
``ballet_guard.ok`` has a ``CHECK (ok = 1)`` constraint named
``ballet_conflict``. When the guarded statement changed no row (or more
than one), the constraint fails, which aborts the batch in SQLite and in
rqlite alike; ``sqlstore`` maps that error to ``ErrConflict``. The check
is pure SQL, so it works identically when the batch is sent to rqlite.

Migrations
----------

Each service embeds its migrations and applies them at startup:

.. code-block:: go

   //go:embed migrations/*.sql
   var migrations embed.FS

   sub, _ := fs.Sub(migrations, "migrations")
   err := db.Migrate(ctx, sub)

- Files are named ``NNNN_description.sql`` (``0001_tickets.sql``) and
  applied in version order, each atomically with its
  ``schema_migrations`` row.
- Never edit an applied migration; add a new one.
- A migration may contain several statements.

Vector functions
----------------

``sqlstore`` registers Go implementations of the sqlite-vec functions
Ballet uses, with identical names and semantics:

.. list-table::
   :header-rows: 1

   * - Function
     - Meaning
   * - ``vec_f32(x)``
     - JSON array text (``'[0.1, 0.2]'``) → vector blob (little-endian
       ``float32``); blobs pass through
   * - ``vec_distance_cosine(a, b)``
     - ``1 - cosine similarity``: 0 same direction, 1 orthogonal, 2
       opposite

From Go, encode vectors with ``sqlstore.EncodeFloat32``. Similarity
search is an exact scan ordered by distance (no approximate index), see
:doc:`/architecture/decisions/0021-hybrid-vector-search-over-all-content`.

Tests
-----

Tests run against real databases:

.. code-block:: go

   db := sqlstoretest.New(t, migrationsFS) // temp file, migrated, closed after the test

Concurrency-sensitive code is tested with several goroutines and
``-race``.
