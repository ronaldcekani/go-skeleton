-- +goose Up
CREATE TABLE items (
    id          UUID PRIMARY KEY,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX items_created_at_idx ON items (created_at DESC);

-- +goose Down
DROP TABLE items;
