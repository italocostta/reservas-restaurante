-- btree_gist vive no schema `extensions` (convenção Supabase), e a EXCLUDE
-- abaixo precisa enxergar o opclass gist_uuid_ops que ele cria. O search_path
-- do Supabase já inclui `extensions`, mas fixamos aqui para não depender disso.
SET search_path = public, extensions;

CREATE EXTENSION IF NOT EXISTS btree_gist WITH SCHEMA extensions;

CREATE TABLE reservations (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    table_id       uuid        NOT NULL REFERENCES restaurant_tables (id),
    customer_name  text        NOT NULL,
    customer_phone text        NOT NULL,
    party_size     smallint    NOT NULL CHECK (party_size > 0),
    starts_at      timestamptz NOT NULL,
    ends_at        timestamptz NOT NULL,
    status         text        NOT NULL DEFAULT 'confirmed'
                               CHECK (status IN ('confirmed', 'cancelled')),
    created_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT ends_after_starts CHECK (ends_at > starts_at)
);

-- Núcleo técnico do projeto: duas reservas confirmed na mesma mesa não podem
-- ter intervalos sobrepostos. tstzrange (não tsrange) porque as colunas são
-- timestamptz. Limites [) por padrão: 19h-20h não colide com 20h-21h.
-- O WHERE torna a constraint parcial — reservas cancelled saem do índice e
-- liberam o horário, que é a razão de `status` existir desde a Fase 1a.
ALTER TABLE reservations
    ADD CONSTRAINT no_overlapping_reservations
    EXCLUDE USING gist (
        table_id WITH =,
        tstzrange(starts_at, ends_at) WITH &&
    )
    WHERE (status = 'confirmed');

ALTER TABLE reservations ENABLE ROW LEVEL SECURITY;
