-- +goose Up
-- Lists and searches order by (created_at DESC, id DESC). The index has to
-- cover both columns to return a page without sorting.
CREATE INDEX items_created_at_id_idx ON items (created_at DESC, id DESC);
DROP INDEX items_created_at_idx;

-- +goose Down
CREATE INDEX items_created_at_idx ON items (created_at DESC);
DROP INDEX items_created_at_id_idx;
