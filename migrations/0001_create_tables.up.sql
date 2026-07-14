CREATE TABLE restaurant_tables (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text        NOT NULL UNIQUE,
    capacity   smallint    NOT NULL CHECK (capacity > 0),
    is_active  boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Sem policies: nega todo acesso via PostgREST (roles anon/authenticated),
-- que o Supabase expõe automaticamente para o schema public. A API em Go
-- conecta como role postgres, que tem BYPASSRLS — não é afetada.
ALTER TABLE restaurant_tables ENABLE ROW LEVEL SECURITY;
