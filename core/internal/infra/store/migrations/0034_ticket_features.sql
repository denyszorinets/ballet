-- The features a ticket changes (ADR-0028): tickets derive from features.
CREATE TABLE ticket_features (
    item_id     TEXT NOT NULL REFERENCES items (id),
    feature_id  TEXT NOT NULL REFERENCES features (id),
    created_at  TEXT NOT NULL,
    PRIMARY KEY (item_id, feature_id)
);
CREATE INDEX ticket_features_feature ON ticket_features (feature_id);
