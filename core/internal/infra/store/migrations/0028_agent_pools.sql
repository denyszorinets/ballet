-- The agent pool a project's runs execute on (ADR-0025); '': any agent.
ALTER TABLE project_execution ADD COLUMN pool TEXT NOT NULL DEFAULT '';
