DROP INDEX IF EXISTS notifications_fila_idx;

CREATE INDEX notifications_pendentes_idx
    ON notifications (created_at)
    WHERE status = 'pending';

ALTER TABLE notifications DROP COLUMN IF EXISTS claimed_at;

ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_status_check;

ALTER TABLE notifications
    ADD CONSTRAINT notifications_status_check
    CHECK (status IN ('pending', 'sent', 'failed'));
