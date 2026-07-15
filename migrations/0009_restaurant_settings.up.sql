-- Expediente e dias de funcionamento saem do .env e passam a viver no banco,
-- editáveis em runtime. Até aqui SERVICE_START/END/TZ eram variáveis de ambiente
-- lidas uma vez no boot (config.Load); a partir daqui o .env só as usa como
-- semente inicial, e a fonte da verdade é esta tabela.

-- restaurant_settings é SINGLETON: uma linha só, o restaurante único (premissa da
-- seção 1). O id fixo em 1 com CHECK é o idioma para "esta tabela tem no máximo
-- uma linha" — sem ele, um segundo INSERT criaria uma segunda config fantasma que
-- ninguém sabe qual vale.
CREATE TABLE restaurant_settings (
    id smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),

    service_start time NOT NULL,
    service_end   time NOT NULL,
    service_tz    text NOT NULL,

    -- Dias da semana em que o restaurante opera, na convenção do Postgres
    -- (EXTRACT(DOW): 0=domingo … 6=sábado). Array e não 7 colunas booleanas: a
    -- pergunta que o código faz é "o dia D está aberto?", que é `dow = ANY(...)`,
    -- e um array responde isso direto. Sete colunas exigiriam um switch.
    open_weekdays smallint[] NOT NULL,

    -- O intervalo tem que ser válido, igual à validação que o config fazia no boot.
    CONSTRAINT expediente_coerente CHECK (service_end > service_start),

    -- Todo valor do array precisa ser um dia da semana real. Sem isto, um {9}
    -- entraria e nunca casaria com dow nenhum, fechando o restaurante em silêncio.
    CONSTRAINT dias_validos CHECK (
        open_weekdays <@ ARRAY[0,1,2,3,4,5,6]::smallint[]
    )
);

-- Semente: exatamente os defaults que o config.go usava, para a troca env→banco
-- ser invisível. Sete dias abertos.
INSERT INTO restaurant_settings (id, service_start, service_end, service_tz, open_weekdays)
VALUES (1, '18:00', '23:00', 'America/Sao_Paulo', ARRAY[0,1,2,3,4,5,6]::smallint[]);

-- Exceções: datas que fogem da regra semanal. Uma data fechada num dia
-- normalmente aberto (feriado), ou aberta num dia normalmente fechado (véspera
-- especial). is_open diz qual dos dois.
CREATE TABLE service_exceptions (
    day     date PRIMARY KEY,
    is_open boolean NOT NULL,
    note    text
);

-- RLS nas duas, igual a todas as outras tabelas (seção 7): sem isto, o PostgREST
-- do Supabase exporia a config do restaurante — e a EDIÇÃO dela — pela chave anon
-- pública. A API em Go usa o role postgres, que tem BYPASSRLS; a anon fica
-- bloqueada com zero policies.
ALTER TABLE restaurant_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE service_exceptions  ENABLE ROW LEVEL SECURITY;
