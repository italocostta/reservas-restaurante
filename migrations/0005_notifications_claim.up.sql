-- A 0004 modelou a ESCRITA do outbox e esqueceu o CONSUMO. Faltam duas coisas
-- para o worker conseguir enviar sem segurar uma transação aberta:
--
-- 1. Um estado intermediário. O worker precisa REIVINDICAR a linha (marcá-la
--    como sua) e só então enviar. Se ele mantivesse a transação aberta durante o
--    envio, uma conexão de banco ficaria travada pela duração de uma chamada
--    HTTP externa — inaceitável num pool.
--
-- 2. Um visibility timeout. Se o processo morrer entre reivindicar e enviar, a
--    linha ficaria presa em 'sending' para sempre. O claimed_at permite que a
--    fila a devolva depois de um tempo, como o SQS faz.

ALTER TABLE notifications DROP CONSTRAINT notifications_status_check;

ALTER TABLE notifications
    ADD CONSTRAINT notifications_status_check
    CHECK (status IN ('pending', 'sending', 'sent', 'failed'));

ALTER TABLE notifications ADD COLUMN claimed_at timestamptz;

-- A fila agora são as pendentes MAIS as reivindicadas há muito tempo (órfãs de
-- um processo morto). O índice parcial cobre as duas.
DROP INDEX notifications_pendentes_idx;

CREATE INDEX notifications_fila_idx
    ON notifications (created_at)
    WHERE status IN ('pending', 'sending');
