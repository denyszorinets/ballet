-- Customers are organizations (ADR-0027): an entry's knowledge space is
-- its organization.
ALTER TABLE entries RENAME COLUMN customer TO organization;
DROP INDEX entries_customer;
CREATE INDEX entries_organization ON entries (organization, kind, updated_at);

ALTER TABLE entry_embeddings RENAME COLUMN customer TO organization;
DROP INDEX entry_embeddings_customer;
CREATE INDEX entry_embeddings_organization ON entry_embeddings (organization, model);
