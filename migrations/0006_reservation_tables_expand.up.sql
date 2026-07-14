-- FASE 3a — EXPAND (parallel change). Esta migration NÃO derruba nada.
--
-- Ela cria a estrutura nova ao lado da antiga e a preenche. A constraint velha
-- (no_overlapping_reservations, em `reservations`) continua ativa. Em nenhum
-- instante o banco fica sem proteção contra overbooking — que é a única coisa
-- que este projeto não pode se dar ao luxo de perder nem por um segundo.
--
-- O contract (derrubar a velha) vai na 0007, e só depois de verificado.

CREATE TABLE reservation_tables (
    reservation_id uuid NOT NULL REFERENCES reservations (id) ON DELETE CASCADE,
    table_id       uuid NOT NULL REFERENCES restaurant_tables (id),

    -- Desnormalizados de `reservations`, e não é preguiça: a EXCLUDE precisa do
    -- intervalo NA MESMA LINHA que o table_id. Constraint não faz JOIN. Sem
    -- essas três colunas aqui, a garantia de não-sobreposição simplesmente não
    -- pode ser expressa no banco — teria que virar código de aplicação, e aí
    -- estaria sujeita a race condition de novo.
    starts_at timestamptz NOT NULL,
    ends_at   timestamptz NOT NULL,
    status    text        NOT NULL,

    PRIMARY KEY (reservation_id, table_id)
);

-- Backfill: cada reserva atual vira exatamente uma linha (1 reserva = 1 mesa).
INSERT INTO reservation_tables (reservation_id, table_id, starts_at, ends_at, status)
SELECT id, table_id, starts_at, ends_at, status
FROM reservations
WHERE table_id IS NOT NULL;

-- A constraint nova. Ela é criada DEPOIS do backfill de propósito: se os dados
-- copiados violarem a regra, o CREATE falha aqui, a transação inteira da
-- migration é revertida, e a constraint antiga continua no lugar. O banco te
-- avisa que o backfill está errado antes de você perder a rede de segurança.
ALTER TABLE reservation_tables
    ADD CONSTRAINT no_overlapping_reservation_tables
    EXCLUDE USING gist (
        table_id WITH =,
        tstzrange(starts_at, ends_at) WITH &&
    )
    WHERE (status = 'confirmed');

-- As colunas desnormalizadas precisam seguir a reserva. Só o `status` muda hoje
-- (cancelamento), mas o trigger cobre os três — um endpoint futuro que remarque
-- horário não pode furar a constraint por esquecimento de quem o escrever.
--
-- POR QUE UM TRIGGER, se o projeto inteiro recusa mágica invisível: porque este
-- é o invariante que NÃO PODE falhar. Sincronizar na aplicação significa que um
-- UPDATE feito por uma migration futura, por um psql manual, ou por um endpoint
-- novo que alguém esquecer de ajustar, deixa a EXCLUDE protegendo dados velhos —
-- em silêncio. Invariante que precisa valer independentemente de quem escreve
-- mora no banco. É o mesmo critério dos CHECK.
CREATE FUNCTION sync_reservation_tables() RETURNS trigger AS $$
BEGIN
    UPDATE reservation_tables
    SET status    = NEW.status,
        starts_at = NEW.starts_at,
        ends_at   = NEW.ends_at
    WHERE reservation_id = NEW.id;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER reservations_sync_tables
    AFTER UPDATE ON reservations
    FOR EACH ROW
    WHEN (OLD.status    IS DISTINCT FROM NEW.status
       OR OLD.starts_at IS DISTINCT FROM NEW.starts_at
       OR OLD.ends_at   IS DISTINCT FROM NEW.ends_at)
    EXECUTE FUNCTION sync_reservation_tables();

-- table_id deixa de ser obrigatória: as reservas NOVAS não vão preenchê-la.
-- A EXCLUDE antiga ignora linhas com table_id NULL (o operador `=` devolve NULL,
-- e exclusion constraint não considera isso conflito), então ela simplesmente
-- para de opinar sobre as reservas novas, sem estorvar. As antigas seguem
-- protegidas pelas DUAS constraints até a 0007.
ALTER TABLE reservations ALTER COLUMN table_id DROP NOT NULL;

ALTER TABLE reservation_tables ENABLE ROW LEVEL SECURITY;
