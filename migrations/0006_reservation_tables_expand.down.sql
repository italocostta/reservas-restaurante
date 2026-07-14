-- Reverter o expand só é seguro enquanto NENHUMA reserva combinada existir: uma
-- reserva de duas mesas não cabe na coluna table_id, e o down a perderia em
-- silêncio. O UPDATE abaixo restaura table_id a partir da junção; se alguma
-- reserva tiver mais de uma mesa, ele estoura por violação de unicidade da
-- subquery — que é o comportamento certo. Melhor falhar do que apagar dado.
UPDATE reservations r
SET table_id = (
    SELECT rt.table_id
    FROM reservation_tables rt
    WHERE rt.reservation_id = r.id
)
WHERE r.table_id IS NULL;

ALTER TABLE reservations ALTER COLUMN table_id SET NOT NULL;

DROP TRIGGER IF EXISTS reservations_sync_tables ON reservations;
DROP FUNCTION IF EXISTS sync_reservation_tables();

DROP TABLE IF EXISTS reservation_tables;
