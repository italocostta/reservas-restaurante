-- Reverter o contract só é seguro enquanto não existir reserva COMBINADA: ela
-- ocupa mais de uma mesa, e nenhuma delas cabe numa coluna table_id única.
--
-- O UPDATE abaixo usa uma subquery escalar de propósito: se alguma reserva tiver
-- duas ou mais mesas, o Postgres estoura com "more than one row returned by a
-- subquery used as an expression" — e a migration inteira reverte. É o
-- comportamento certo. Melhor falhar alto do que escolher uma mesa em silêncio e
-- perder a outra.
ALTER TABLE reservations ADD COLUMN table_id uuid REFERENCES restaurant_tables (id);

UPDATE reservations r
SET table_id = (
    SELECT rt.table_id
    FROM reservation_tables rt
    WHERE rt.reservation_id = r.id
);

ALTER TABLE reservations
    ADD CONSTRAINT no_overlapping_reservations
    EXCLUDE USING gist (
        table_id WITH =,
        tstzrange(starts_at, ends_at) WITH &&
    )
    WHERE (status = 'confirmed');
