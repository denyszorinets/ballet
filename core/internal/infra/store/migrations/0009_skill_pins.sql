-- How a project uses skills: version pins (0 = latest) and exclusions.
CREATE TABLE skill_pins (
    project_id  TEXT NOT NULL REFERENCES projects (id),
    name        TEXT NOT NULL,
    version     INTEGER NOT NULL DEFAULT 0,
    disabled    INTEGER NOT NULL DEFAULT 0,
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (project_id, name)
);
