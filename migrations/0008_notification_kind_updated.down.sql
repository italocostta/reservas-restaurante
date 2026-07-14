-- Volta ao CHECK de dois valores. Isto FALHA de propósito se já existir alguma
-- notificação 'reservation_updated' no banco: o ADD CONSTRAINT valida as linhas
-- existentes e recusa. É a rede de segurança certa — reverter para um predicado
-- que os dados violam tem que dar erro, não apagar dado em silêncio (mesmo
-- princípio dos downs da 0006/0007).
ALTER TABLE notifications
    DROP CONSTRAINT notifications_kind_check;

ALTER TABLE notifications
    ADD CONSTRAINT notifications_kind_check
    CHECK (kind IN ('reservation_confirmed', 'reservation_cancelled'));
