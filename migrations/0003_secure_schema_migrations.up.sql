-- O golang-migrate cria schema_migrations sozinho, no schema public e sem RLS —
-- ou seja, exposta ao PostgREST que o Supabase publica automaticamente. Sem
-- isto, qualquer um com a chave anon (que é pública, vai no bundle do Vue)
-- poderia reescrever a versão da migration e corromper o estado do schema.
--
-- Seguro para o próprio migrate: ele conecta como role postgres, que tem
-- BYPASSRLS. Quem perde acesso é anon/authenticated, que é o objetivo.
ALTER TABLE schema_migrations ENABLE ROW LEVEL SECURITY;
