-- How a project's runs execute: repository, devcontainer template,
-- workspace preparation (domain/execution). Setup and env are JSON.
CREATE TABLE project_execution (
    project_id       TEXT PRIMARY KEY REFERENCES projects (id),
    repo_url         TEXT NOT NULL,
    default_branch   TEXT NOT NULL,
    image            TEXT NOT NULL,
    setup            TEXT NOT NULL,
    env              TEXT NOT NULL,
    branch_template  TEXT NOT NULL,
    git_name         TEXT NOT NULL,
    git_email        TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    version          INTEGER NOT NULL
);

-- The ticket branch a run works on ('' when the project has no repository).
ALTER TABLE runs ADD COLUMN branch TEXT NOT NULL DEFAULT '';
