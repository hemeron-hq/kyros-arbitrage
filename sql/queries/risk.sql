-- name: GetRiskMode :one
SELECT mode FROM risk_settings WHERE id = 1;

-- name: UpsertRiskMode :exec
INSERT INTO risk_settings (id, mode, updated_at)
VALUES (1, ?, CURRENT_TIMESTAMP)
ON CONFLICT(id) DO UPDATE SET
  mode = excluded.mode,
  updated_at = CURRENT_TIMESTAMP;
