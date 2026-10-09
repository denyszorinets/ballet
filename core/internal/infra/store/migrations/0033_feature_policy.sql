-- How agents may change features (ADR-0028): per organization, and per
-- project ('' inherits the organization's).
ALTER TABLE organizations ADD COLUMN feature_policy TEXT NOT NULL DEFAULT 'direct';
ALTER TABLE project_execution ADD COLUMN feature_policy TEXT NOT NULL DEFAULT '';
