-- A edição de reserva (PATCH /reservations/{id}) enfileira um evento
-- 'reservation_updated' — um só, no lugar do par cancelamento+confirmação que a
-- edição produz por baixo. O CHECK da 0004 não conhecia esse valor e recusaria o
-- INSERT, então ele precisa crescer ANTES de o código passar a emiti-lo.
--
-- Troca do CHECK, não ALTER de enum: `kind` é `text` com CHECK (decisão da 0004),
-- e o jeito de estender um CHECK é derrubá-lo e recriá-lo. Barato: a coluna não
-- muda, os dados existentes ('confirmed'/'cancelled') continuam válidos sob o
-- predicado novo, e não há reescrita de tabela.
ALTER TABLE notifications
    DROP CONSTRAINT notifications_kind_check;

ALTER TABLE notifications
    ADD CONSTRAINT notifications_kind_check
    CHECK (kind IN ('reservation_confirmed', 'reservation_cancelled', 'reservation_updated'));
