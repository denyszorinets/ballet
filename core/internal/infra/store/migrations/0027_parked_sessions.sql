-- Questions answered online (ADR-0026): how long a project's sessions wait
-- for answers before they park (0: Ballet's default), and what continues a
-- parked session (the runtime's transcript, gzip, base64).
ALTER TABLE project_execution ADD COLUMN answer_window_minutes INTEGER NOT NULL DEFAULT 0;

CREATE TABLE run_states (
    run_id  TEXT PRIMARY KEY REFERENCES runs (id),
    data    TEXT NOT NULL
);
