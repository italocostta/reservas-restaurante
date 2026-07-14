-- FASE 3a — CONTRACT (a segunda metade do parallel change).
--
-- A 0006 construiu a estrutura nova ao lado da velha. Entre as duas migrations,
-- o código foi trocado, o comportamento foi verificado contra o banco real, e a
-- constraint nova provou que bloqueia sobreposição (23P01) e que libera o horário
-- no cancelamento. SÓ AGORA a velha sai.
--
-- Esta é a parte que quase todo mundo pula, e é a que separa "migrei o schema" de
-- "migrei o schema sem apagar a luz no meio do caminho".

-- A constraint antiga já não protege nada: as reservas novas têm table_id NULL, e
-- exclusion constraint ignora linhas onde o operador devolve NULL. Ela vinha
-- opinando apenas sobre as três reservas anteriores à migração.
ALTER TABLE reservations DROP CONSTRAINT no_overlapping_reservations;

-- A coluna vira mentira: uma reserva combinada ocupa duas mesas, e nenhuma delas
-- cabe aqui. Manter a coluna seria manter um campo que ora diz a verdade, ora
-- está NULL, ora estaria escolhendo arbitrariamente uma das mesas. Coluna que
-- mente é pior que coluna que não existe.
ALTER TABLE reservations DROP COLUMN table_id;
