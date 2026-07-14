-- Outbox transacional. A linha aqui é gravada na MESMA transação do INSERT/UPDATE
-- da reserva: ou existem as duas, ou nenhuma. Se o processo morrer antes de
-- enviar, a linha continua aqui e alguém pega depois — que é o que um channel
-- em memória não consegue prometer.
CREATE TABLE notifications (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    reservation_id uuid        NOT NULL REFERENCES reservations (id),

    kind text NOT NULL
        CHECK (kind IN ('reservation_confirmed', 'reservation_cancelled')),

    -- Snapshot do que precisa ser enviado, congelado no instante do evento.
    -- Não é um JOIN com reservations de propósito: uma notificação de
    -- "confirmada" despachada com atraso não pode sair descrevendo o estado
    -- atual da reserva, que já pode ter sido cancelada. O outbox carrega o
    -- fato como ele foi, não como ele está.
    payload jsonb NOT NULL,

    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'sent', 'failed')),

    attempts   smallint    NOT NULL DEFAULT 0,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at    timestamptz,

    -- sent_at só existe se de fato foi enviada. Impede o estado incoerente
    -- "status = sent, sent_at = NULL", que o app poderia produzir por bug.
    CONSTRAINT sent_at_coerente CHECK (
        (status = 'sent' AND sent_at IS NOT NULL) OR
        (status <> 'sent' AND sent_at IS NULL)
    )
);

-- Índice PARCIAL: a fila é sempre "as pendentes, mais antigas primeiro". As
-- enviadas (que vão ser a esmagadora maioria com o tempo) não entram no índice
-- e não custam nada. Mesma ideia da EXCLUDE parcial das reservas.
CREATE INDEX notifications_pendentes_idx
    ON notifications (created_at)
    WHERE status = 'pending';

ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
