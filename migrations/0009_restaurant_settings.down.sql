-- Derruba as duas tabelas. Diferente dos downs da 0006/0007, aqui não há risco de
-- perda silenciosa de dado de reserva: settings e exceptions são config, não
-- histórico. O expediente volta a ser responsabilidade do .env (SERVICE_START/END/
-- TZ), que é para onde o código revertido volta a olhar.
DROP TABLE IF EXISTS service_exceptions;
DROP TABLE IF EXISTS restaurant_settings;
