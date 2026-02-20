-- +goose Up
ALTER TABLE sessions ADD COLUMN response TEXT;
ALTER TABLE sessions ADD COLUMN cost_usd REAL;
ALTER TABLE sessions ADD COLUMN num_turns INTEGER;
ALTER TABLE sessions ADD COLUMN duration_ms INTEGER;

-- +goose Down
ALTER TABLE sessions DROP COLUMN duration_ms;
ALTER TABLE sessions DROP COLUMN num_turns;
ALTER TABLE sessions DROP COLUMN cost_usd;
ALTER TABLE sessions DROP COLUMN response;
