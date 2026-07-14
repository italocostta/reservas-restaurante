# Sistema de Reservas de Restaurante — Especificação Técnica

> Projeto de estudo da linguagem Go. Backend em Go puro (sem framework web na Fase 1a/1b), Postgres via Supabase, frontend Vue consumindo via REST.

---

## 0. Protocolo de execução — LEIA ANTES DE IMPLEMENTAR

**Instrução para o Claude Code (ou qualquer agente implementando esta spec):**

Este é um projeto de **estudo guiado**, não uma entrega de produto. O autor é um desenvolvedor Jr aprendendo Go e quer acompanhar e entender cada peça antes de avançar para a próxima — não quer o código pronto de uma vez. Siga as regras abaixo estritamente.

### Regra de granularidade

Implemente **uma unidade de trabalho por vez**, nesta ordem de granularidade decrescente de parada obrigatória:

1. **Nunca implemente mais de um módulo (`internal/<pacote>`) sem parar.**
2. **Dentro de um módulo, nunca implemente mais de um arquivo sem parar** (ex: não escreva `model.go` e `repository.go` na mesma resposta).
3. **Dentro de um arquivo com múltiplas funções não-triviais, pare após cada função** e aguarde confirmação antes de escrever a próxima — especialmente em `allocator.go` (Fase 1b/1c), onde cada função tem uma decisão de design que vale explicar isoladamente (ex: a função de heurística gulosa, depois a função de retry, depois a função de checagem de erro Postgres — três paradas, não uma).

Structs simples de modelo (`model.go` com só campos, sem métodos) podem ser escritas inteiras de uma vez — não há decisão de design por campo que justifique parar a cada linha.

### O que fazer em cada parada

Após cada unidade de trabalho (arquivo ou função, conforme regra acima):

1. Mostrar o código escrito.
2. Explicar em 2-4 frases **o que a decisão de design ali resolve** — não uma descrição do que o código faz linha a linha (isso o autor já lê sozinho), mas o motivo daquela abordagem existir, especialmente quando ela reflete uma escolha já registrada nesta spec (ex: por que `allocator.go` não importa `net/http`, seção 4).
3. Se a unidade implementada tem um paralelo direto com algo do ecossistema Java/Spring que o autor conhece, apontar a diferença brevemente (ex: "isso substitui o que seria `@Repository` + `JpaRepository<T>` no Spring, mas aqui a interface é definida por quem consome, não por quem provê").
4. **Parar e aguardar resposta do autor antes de continuar.** Não seguir para o próximo arquivo/função automaticamente, mesmo que o próximo passo pareça óbvio ou trivial.

### Ordem de implementação entre módulos

Seguir a ordem das fases desta spec (seção 1): dentro da Fase 1a, implementar `table/` completo (com testes) antes de iniciar `reservation/`, já que `reservation` depende conceitualmente de `table` existir. Não pular para Fase 1b/1c sem a fase anterior ter testes passando.

### O que NÃO fazer

- Não gerar múltiplos arquivos "para adiantar" mesmo que pareça mais eficiente.
- Não pular a explicação de design achando que o código é autoexplicativo — o objetivo declarado é aprendizado da linguagem, não só ter o sistema funcionando.
- Não silenciosamente corrigir ou desviar de uma decisão registrada nesta spec (ex: trocar lock otimista por `SELECT FOR UPDATE` porque "é mais simples de implementar"). Se identificar um problema real numa decisão da spec, **apontar o problema explicitamente e perguntar antes de desviar**, não decidir sozinho.
- Não avançar de fase (1a → 1b → 1c) sem o autor confirmar que quer avançar.

---

## 1. Visão geral

API backend para gerenciamento de reservas de mesas em um restaurante único (sem multi-tenant). Staff cria reservas para clientes; sistema aloca mesa automaticamente respeitando capacidade, horário e evitando conflitos de sobreposição — com opção de override manual.

### Fora de escopo (explicitamente adiado)

- Multi-restaurante / multi-tenant
- Autenticação/autorização (staff assumido confiável nesta fase; fica como extensão futura)
- Reserva feita pelo próprio cliente via app (fluxo é staff-only)
- **Combinação/partição de mesas** (juntar mesas fisicamente adjacentes para aproveitar capacidade "sobrando" — ex: mesa de 8 com grupo de 4 não pode ceder os 4 lugares restantes para outro grupo). Modelo assume 1 reserva = 1 mesa inteira ocupada. Revisitar apenas em fase futura dedicada, pois exige redesenho do modelo de dados (tabela de adjacência, constraint `EXCLUDE` diferente).
- Otimização de alocação além de heurística gulosa simples (sem bin-packing ótimo, sem replanejamento de reservas já confirmadas)
- Notificações (email/SMS) — gancho para Fase 2 (worker pool assíncrono)
- Pagamento, no-show tracking, lista de espera
- Machine-readable error codes (`error.code`) — v1 usa só mensagem humana em `error` string; migração para código estruturado é breaking change de contrato, fica registrada como débito técnico intencional para fase futura

### Sub-fases de implementação

| Fase | Escopo | Objetivo de aprendizado |
|---|---|---|
| **1a** | CRUD de mesas e reservas, mesa atribuída manualmente, constraint de overlap no Postgres | `net/http` puro, `database/sql`/`pgx`, modelagem de intervalo de tempo com `tstzrange`/`EXCLUDE` |
| **1b** | Alocação automática de mesa (heurística gulosa via SQL), com override manual opcional | Separação de domínio e HTTP via interfaces definidas pelo consumidor; testes de unidade de validação e roteamento de erro sem banco |
| **1c** | Concorrência: múltiplas requisições simultâneas competindo pela mesma mesa/horário | Estratégia otimista + retry, `go test -race`, teste de integração concorrente |
| **2** | Worker pool para notificação assíncrona pós-reserva | goroutines, channels, `context` para timeout |
| **3 (futuro, não planejado em detalhe)** | Combinação de mesas | Redesenho de modelo — só após 1a/1b/1c estarem sólidas |

---

## 2. Modelo de dados (Fase 1a)

### `restaurant_tables`

| Coluna | Tipo | Regra |
|---|---|---|
| `id` | `uuid` | PK, `gen_random_uuid()` |
| `name` | `text` | Único (ex: "Mesa 12") |
| `capacity` | `smallint` | `> 0` |
| `is_active` | `boolean` | Default `true` |
| `created_at` | `timestamptz` | Default `now()` |

### `reservations`

| Coluna | Tipo | Regra |
|---|---|---|
| `id` | `uuid` | PK, `gen_random_uuid()` |
| `table_id` | `uuid` | FK → `restaurant_tables.id`, `NOT NULL` |
| `customer_name` | `text` | `NOT NULL` |
| `customer_phone` | `text` | `NOT NULL` (sem validação de formato na v1) |
| `party_size` | `smallint` | `> 0`; validado em app: `<= table.capacity` |
| `starts_at` | `timestamptz` | `NOT NULL` |
| `ends_at` | `timestamptz` | `NOT NULL`, `CHECK (ends_at > starts_at)` |
| `status` | `text` | `confirmed` \| `cancelled` (soft delete), `CHECK (status IN ('confirmed', 'cancelled'))` |
| `created_at` | `timestamptz` | Default `now()` |

### Constraint de overlap (núcleo técnico do projeto)

```sql
CREATE EXTENSION IF NOT EXISTS btree_gist WITH SCHEMA extensions;

ALTER TABLE reservations
  ADD CONSTRAINT no_overlapping_reservations
  EXCLUDE USING gist (
    table_id WITH =,
    tstzrange(starts_at, ends_at) WITH &&
  )
  WHERE (status = 'confirmed');
```

**Correção sobre versão anterior desta spec:** a função correta é `tstzrange`, não `tsrange`. `starts_at`/`ends_at` são `timestamptz`; `tsrange` espera `timestamp` (sem timezone) e o cast entre os dois é `assignment`, não `implicit` — resolução de função do Postgres só considera casts implícitos, então `tsrange(starts_at, ends_at)` falha em tempo de migration com `function tsrange(timestamp with time zone, timestamp with time zone) does not exist`. `tstzrange` recebe `timestamptz` diretamente.

`WITH SCHEMA extensions` é convenção do Supabase para extensões (evita poluir o schema `public`).

Impede duas reservas `confirmed` na mesma mesa com intervalos sobrepostos. A cláusula `WHERE (status = 'confirmed')` é o motivo de `status` existir desde a 1a: sem ela, cancelamento (soft delete) não liberaria o horário para reuso.

**Semântica do intervalo — decisão explícita, não acidente:** `tstzrange` por padrão usa limites `[)` (inclusivo no início, exclusivo no fim). Isso significa que uma reserva das 19:00–20:00 **não** colide com outra das 20:00–21:00 — o horário de término de uma libera exatamente no instante em que a próxima começa. É o comportamento desejado para este domínio (turnos consecutivos na mesma mesa) e fica registrado aqui como escolha, não como default não examinado.

**Cancelamento = soft delete** (`status = cancelled`), decisão confirmada — mantém histórico/auditoria.

---

## 3. Endpoints e validações (Fase 1a)

### Mesas

| Método | Rota | Descrição |
|---|---|---|
| `POST` | `/tables` | Cria mesa |
| `GET` | `/tables` | Lista (filtro opcional `?active=true`) |
| `GET` | `/tables/{id}` | Detalhe |
| `PATCH` | `/tables/{id}` | Atualiza (ex: desativar) |

### Reservas

| Método | Rota | Descrição |
|---|---|---|
| `POST` | `/reservations` | Cria reserva (ver contrato na seção 4 para 1b) |
| `GET` | `/reservations` | Lista, filtros `?date=`, `?table_id=`, `?status=` |
| `GET` | `/reservations/{id}` | Detalhe |
| `DELETE` | `/reservations/{id}` | Cancela (soft delete → `status = cancelled`) |
| `GET` | `/tables/{id}/availability?date=` | Janelas livres da mesa no dia, dentro do horário de funcionamento (ver seção 7 — `SERVICE_START`/`SERVICE_END`/`TZ`) |

### Validações em camada de aplicação (antes do banco)

A constraint SQL é a rede de segurança final contra race condition (resolvida de fato na 1c), não o lugar para mensagem de erro amigável.

Na criação de reserva:
1. `party_size > 0`
2. `starts_at < ends_at`
3. `starts_at` não pode ser no passado — **implementar via `Clock` injetável** (`interface { Now() time.Time }`), não `time.Now()` chamado direto dentro da função de validação. Sem isso, testes de unidade para este caso precisam de datas fixas no futuro que "expiram" sozinhas com o tempo, quebrando build meses depois sem relação com o código em si
4. Mesa existe e `is_active = true` (quando `table_id` for informado)
5. `party_size <= table.capacity`
6. Checagem de overlap em app antes do `INSERT` (sabidamente com race condition na 1a — resolvida na 1c)
7. Se a constraint do banco disparar mesmo assim (`23P01` — exclusion_violation), retornar `409 Conflict` com mensagem legível, nunca erro bruto do Postgres
8. **`starts_at` deve estar dentro do horário de funcionamento** (`SERVICE_START` ≤ hora de `starts_at` convertida para `SERVICE_TZ` < `SERVICE_END`, seção 7). `ends_at` **pode** ultrapassar `SERVICE_END` — decisão deliberada para permitir a última reserva do dia (ex: mesa que senta às 22h30 com fechamento às 23h, terminando à meia-noite). Sem esta validação, o sistema aceitaria reservas fora do expediente (ex: 03h da manhã) que o endpoint `/availability` nunca refletiria como ocupação real, gerando inconsistência observável entre o que existe no banco e o que a UI mostra como disponível

### Formato de erro (v1 — mensagem humana apenas)

```json
{
  "error": "Grupo de 6 pessoas excede a capacidade da mesa (4)."
}
```

Sem campo `code`. Migração para machine-readable é decisão futura e será breaking change de contrato (frontend Vue precisará ser ajustado junto).

---

## 4. Alocação automática (Fase 1b)

### Contrato do `POST /reservations` (híbrido)

```json
{
  "table_id": "uuid | null",
  "customer_name": "string",
  "customer_phone": "string",
  "party_size": "int",
  "starts_at": "timestamp",
  "ends_at": "timestamp"
}
```

- `table_id` informado → pula heurística, roda validação manual da 1a
- `table_id` nulo/omitido → roda heurística de alocação automática

### Heurística gulosa

1. Buscar mesas `is_active = true` com `capacity >= party_size`
2. Filtrar as sem overlap de horário (mesma checagem da 1a)
3. Ordenar por `capacity ASC` (menor mesa suficiente primeiro)
4. Alocar a primeira; se nenhuma sobrar, `409` com "sem disponibilidade para o horário solicitado"

### Limitação conhecida (não é bug)

Alocação sequencial pode gerar desperdício de capacidade que a heurística **não corrige retroativamente** (não reorganiza reservas já confirmadas). Exemplo: grupo de 2 ocupa a única mesa de 4 disponível; grupo de 4 que chega depois só encontra mesa de 8, desperdiçando 4 lugares. Resolver isso de verdade exige combinação/partição de mesas — fora de escopo (seção 1), fica para fase futura.

### Separação de responsabilidade (crítico para Fase 1c)

A lógica de escolha de mesa **não mora no handler HTTP**. Handler decodifica request e chama uma função de domínio.

**Segunda correção sobre versão anterior desta spec** — a primeira correção (interfaces em vez de `*sql.DB`) resolveu o acoplamento a driver concreto na assinatura, mas deixou três problemas que só apareceram ao tentar de fato implementar o `POST /reservations` contra essas interfaces:

1. **`Insert` devolvia só `error`.** O `id` da reserva é gerado pelo banco (`gen_random_uuid()`); sem o repositório devolver a `Reservation` completa (via `RETURNING id` no SQL), o handler não tem o que colocar no corpo do `201 Created`.
2. **A função se chamava `AllocateTable` e devolvia `Table`, mas o endpoint cria e devolve uma *reserva*.** Renomeada para `CreateReservation`, devolvendo `(Reservation, error)` — reflete o que ela de fato faz (escolhe mesa **e** insere), o que também é necessário para o retry da Fase 1c continuar encapsulado dentro dela.
3. **Faltava um método para buscar a mesa por id no caminho manual.** `tryAllocateSpecific` precisa validar `is_active` e `capacity` da mesa pedida antes de tentar o `INSERT` — isso exige `GetByID`, que a versão anterior não listava.

Além disso, `isExclusionViolation` (seção 5) inspecionava `pgconn.PgError` diretamente — o que significa que `allocator.go` continuava importando o driver Postgres, só que indiretamente, contrariando o próprio objetivo da interface. **A correção é tradução de erro na fronteira**: só a implementação concreta de `ReservationCreator` (em `reservation/repository.go`) importa `pgconn`; ela traduz `23P01` para um erro sentinela de domínio antes de devolver. `allocator.go` nunca importa nada de `pgx`.

```go
// reservation/errors.go — erros de domínio, sem dependência de driver
var (
    ErrSlotTaken        = errors.New("horário já ocupado nessa mesa")
    ErrNoAvailability   = errors.New("nenhuma mesa disponível para o horário solicitado")
    ErrTableUnavailable = errors.New("a mesa solicitada já está reservada para esse horário")
)

// reservation/repository.go — único arquivo do pacote que importa pgconn
func (r *PostgresRepo) Insert(ctx context.Context, res Reservation) (Reservation, error) {
    err := r.db.QueryRow(ctx, insertSQL, ...).Scan(&res.ID, &res.CreatedAt)
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) && pgErr.Code == "23P01" {
        return Reservation{}, ErrSlotTaken
    }
    if err != nil {
        return Reservation{}, err
    }
    return res, nil
}
```

O mesmo princípio se aplica a `table/repository.go`: uma violação de unicidade em `name` (`23505`) deve virar `table.ErrDuplicateName` ali, não vazar `pgconn.PgError` até o handler.

**Terceira correção sobre versão anterior desta spec — quem implementa `TableFinder`:** a versão anterior deixava implícito, pelo nome e pelo tipo de retorno (`[]Table`), que `table.PostgresRepo` implementaria `TableFinder`. Isso está errado e contradiz um princípio que a própria spec já declarava corretamente na seção 6 ("cada domínio define só os métodos que usa", organização por domínio). `FindCandidates` não é uma pergunta sobre mesas — é uma pergunta sobre disponibilidade de reserva, que usa mesas como dado:

```sql
SELECT t.* FROM restaurant_tables t
LEFT JOIN reservations r ON r.table_id = t.id
  AND r.status = 'confirmed'
  AND tstzrange(r.starts_at, r.ends_at) && tstzrange($2, $3)
WHERE t.is_active = true AND t.capacity >= $1 AND r.id IS NULL
ORDER BY t.capacity ASC;
```

Se `table/repository.go` implementasse isso, o pacote `table` passaria a conhecer a existência de `reservations` — acoplamento de domínio na direção errada. A correção:

- **`table/repository.go` fica só com CRUD de `restaurant_tables`** (seção 3: criar, listar, detalhe, atualizar). Não sabe nada sobre reservas.
- **`reservation/repository.go` implementa as duas interfaces, `TableFinder` e `ReservationCreator`**, num único struct (`PostgresRepo`) — idiomático em Go: o pacote consumidor fatia a interface que precisa; o provedor concreto não precisa antecipar isso.
- **`reservation` importa `table`** para usar o tipo `table.Table` como retorno de `FindCandidates`/`GetByID` — sem ciclo, já que `table` nunca importa `reservation`. O `[]Table` da assinatura é `[]table.Table`.
- **`table` continua não definindo nenhuma interface de repositório** — nem antes, nem agora; quem definia e quem implementava eram os problemas, não a ausência de interface em `table` (isso já estava certo na versão anterior e continua certo).

```go
// reservation/repository.go — implementa as duas interfaces que reservation consome;
// único arquivo do pacote que importa pgconn E que conhece o schema de restaurant_tables
type PostgresRepo struct {
    db *pgxpool.Pool
}

func (r *PostgresRepo) FindCandidates(ctx context.Context, partySize int, starts, ends time.Time) ([]table.Table, error) { ... }
func (r *PostgresRepo) GetByID(ctx context.Context, id uuid.UUID) (table.Table, error) { ... }
func (r *PostgresRepo) Insert(ctx context.Context, res Reservation) (Reservation, error) { ... }
```

Interfaces corrigidas, definidas pelo pacote `reservation` (consumidor), usando `table.Table` em vez de um `Table` local duplicado:

```go
type TableFinder interface {
    FindCandidates(ctx context.Context, partySize int, starts, ends time.Time) ([]table.Table, error)
    GetByID(ctx context.Context, id uuid.UUID) (table.Table, error)
}

type ReservationCreator interface {
    Insert(ctx context.Context, r Reservation) (Reservation, error)
}

func CreateReservation(ctx context.Context, finder TableFinder, creator ReservationCreator, req AllocationRequest) (Reservation, error)
```

### O que a Fase 1b realmente testa sem banco — objetivo reescrito

**Correção de enquadramento, não só de código:** a versão anterior descrevia o objetivo da 1b como "testar a lógica da heurística gulosa" sem banco. Isso está impreciso o suficiente para induzir um teste vazio. A heurística (seção acima: filtrar capacidade, filtrar overlap, ordenar por `capacity ASC`, pegar a primeira) é, na prática, resolvida por uma query SQL (`ORDER BY capacity ASC LIMIT 1` sobre o resultado já filtrado) — não sobra decisão de seleção para testar isoladamente em Go. Um teste de unidade que só define o que o fake `FindCandidates` devolve e confere que `CreateReservation` "escolheu" esse mesmo resultado está testando o fake, não a função.

Fazer o filtro de overlap em Go (trazendo reservas para memória e comparando ali) seria pior engenharia, não solução — descartado por esse motivo, não por preguiça.

O que **tem** substância para testar sem banco na 1b, e passa a ser o objetivo real da fase:
- Validações que antecedem a escolha de mesa: `party_size > 0`, `starts_at < ends_at`, `starts_at` não no passado (via `Clock` injetável), `party_size <= table.capacity` no caminho manual
- Roteamento de erro e decisão de retry (seção 5): caminho manual não retenta e devolve `ErrTableUnavailable`; caminho automático retenta até `maxRetries` e devolve `ErrNoAvailability` ao esgotar
- Isso torna o teste #3 da seção 5 (que confirma que o caminho manual não retenta) o teste central desta fase — não um extra depois da heurística.

---

## 5. Concorrência (Fase 1c)

### O problema

Entre o `SELECT` de mesas candidatas e o `INSERT` da reserva existe uma janela onde duas requisições concorrentes podem ambas considerar a mesma mesa disponível. A constraint `EXCLUDE` garante que uma das duas falhe no banco — falta tratar essa falha corretamente em Go.

### Estratégia escolhida: lock otimista + retry — mas só quando retry faz sentido

Não trava linhas antecipadamente (`SELECT FOR UPDATE` foi avaliado e descartado — geraria contenção desnecessária entre reservas da mesma mesa em horários não sobrepostos, já que o lock pessimista não modela bem uma condição de disponibilidade baseada em intervalo de tempo). Deixa-se as transações concorrentes tentarem `INSERT`; a que perder recebe erro Postgres `23P01` (exclusion_violation), capturado em Go.

**Correção sobre versão anterior desta spec — duas rodadas:**

*Primeira rodada:* a versão anterior retentava sempre e, ao esgotar as tentativas, retornava sempre `ErrNoAvailability` — inclusive quando `table_id` havia sido informado explicitamente (caminho manual, seção 4). Isso estava errado em dois níveis: (1) retentar no caminho manual é trabalho inútil, porque a mesma mesa específica vai colidir de novo a cada tentativa — não há "próxima opção" para reconsultar; (2) a mensagem final mentia sobre a causa.

*Segunda rodada:* a correção da primeira rodada introduziu `isExclusionViolation` verificando `pgconn.PgError` **de dentro do allocator** — o que reabria, por outra porta, o mesmo problema que a introdução das interfaces `TableFinder`/`ReservationCreator` (seção 4) havia acabado de fechar: `allocator.go` voltava a depender do driver Postgres. Isso também tornava impossível escrever o teste #3 abaixo como teste de unidade — um fake em memória não tem por que fabricar um `pgconn.PgError`.

A correção definitiva usa `errors.Is` contra o erro sentinela `ErrSlotTaken`, já traduzido na fronteira pela implementação concreta de `ReservationCreator` (seção 4, `reservation/repository.go` — único ponto do pacote que importa `pgconn`):

```go
func CreateReservation(ctx context.Context, finder TableFinder, creator ReservationCreator, req AllocationRequest) (Reservation, error) {
    // Caminho manual (table_id informado): sem retry.
    // Uma colisão aqui é definitiva — não existe "próxima mesa" a tentar.
    if req.PreferredTableID != nil {
        res, err := tryAllocateSpecific(ctx, finder, creator, req)
        if errors.Is(err, ErrSlotTaken) {
            return Reservation{}, ErrTableUnavailable
        }
        return res, err
    }

    // Caminho automático: retry faz sentido, pois a reconsulta pode
    // encontrar outra mesa candidata que a tentativa anterior não via como ocupada.
    const maxRetries = 3
    for attempt := 0; attempt < maxRetries; attempt++ {
        res, err := tryAllocateAutomatic(ctx, finder, creator, req)
        if err == nil {
            return res, nil
        }
        if errors.Is(err, ErrSlotTaken) {
            continue
        }
        return Reservation{}, err
    }
    return Reservation{}, ErrNoAvailability
}
```

`allocator.go` não importa `pgx` nem `pgconn` em nenhum ponto — só conhece os três erros sentinela de domínio definidos em `reservation/errors.go` (seção 4): `ErrSlotTaken` (sinal interno de colisão, vindo do repositório), `ErrNoAvailability` e `ErrTableUnavailable` (erros finais expostos ao handler).

### O que testar

1. Teste de integração (banco real) disparando N goroutines simultâneas (`sync.WaitGroup`) pedindo o mesmo horário e capacidade que só uma mesa comporta, **sem** `table_id` — caminho automático
2. Assert: exatamente 1 sucesso, N−1 retornam `ErrNoAvailability` — sem pânico, sem deadlock, sem erro genérico
3. Teste equivalente **com** `table_id` fixo igual em todas as goroutines — caminho manual. Assert: exatamente 1 sucesso, N−1 retornam `ErrTableUnavailable` (não `ErrNoAvailability`) e sem retry de fato executado
4. **Este teste #3 pode e deve ser escrito também como teste de unidade** (Fase 1b, sem banco): fake de `ReservationCreator` que devolve `ErrSlotTaken` na primeira chamada é suficiente para confirmar que o caminho manual não chama `Insert` uma segunda vez — não precisa de concorrência real nem de Postgres para essa parte da asserção
5. Executar sempre com `go test -race`
6. Repetir execução (`-count=20`) para reduzir falso-negativo por não-determinismo

### Limitação conhecida: livelock sob alta contenção

`maxRetries = 3` é arbitrário. Sob contenção muito alta (muitas requisições simultâneas para poucas mesas), é possível esgotar os retries mesmo havendo mesa disponível, porque a vaga é tomada a cada nova tentativa. Não é bug — é trade-off consciente da estratégia otimista, aceitável para o volume real de um restaurante (não são dezenas de reservas simultâneas para o mesmo minuto).

---

## 6. Estrutura de pastas

Organização por **domínio**, não por camada técnica (decisão deliberada contra o padrão `controller/service/repository` do Spring). Uso de `internal/` para encapsulamento real (mecanismo do compilador Go, não convenção).

```
reservas-restaurante/
├── cmd/
│   └── api/
│       └── main.go              # monta dependências, sobe servidor
├── internal/
│   ├── table/
│   │   ├── model.go
│   │   ├── repository.go        # implementação Postgres, só CRUD de restaurant_tables — não conhece reservations
│   │   ├── handler.go           # handlers HTTP (/tables)
│   │   └── handler_test.go
│   ├── reservation/
│   │   ├── model.go              # Reservation, AllocationRequest
│   │   ├── errors.go             # ErrSlotTaken, ErrNoAvailability, ErrTableUnavailable
│   │   ├── repository.go         # implementa TableFinder e ReservationCreator (join com restaurant_tables); único arquivo do pacote que importa pgconn/pgx
│   │   ├── allocator.go          # CreateReservation — depende só de TableFinder/ReservationCreator; contém o retry (1c)
│   │   ├── allocator_test.go     # testes de unidade (validações, roteamento de erro) e concorrência
│   │   ├── handler.go            # handlers HTTP (/reservations)
│   │   └── handler_test.go
│   ├── httpserver/
│   │   ├── router.go             # monta rotas, injeta handlers
│   │   ├── middleware.go         # logging, recovery, CORS
│   │   └── response.go           # helpers JSON + erro padrão
│   └── config/
│       └── config.go             # variáveis de ambiente
├── migrations/
│   ├── 0001_create_tables.up.sql
│   ├── 0001_create_tables.down.sql
│   ├── 0002_create_reservations.up.sql
│   └── 0002_create_reservations.down.sql
├── docs/                          # gerado por swaggo — não editar manualmente
├── go.mod
├── go.sum
└── .env.example
```

### Racional das decisões de estrutura

- **`table/` e `reservation/` como pacotes independentes, mas não simétricos**: `table` não sabe que `reservation` existe — `table/repository.go` é só CRUD de `restaurant_tables`. `reservation` importa `table` (nunca o contrário) e seu `repository.go` implementa as interfaces que `reservation` mesmo declara (`TableFinder`, `ReservationCreator`), incluindo consultas que fazem `JOIN` com `restaurant_tables` — porque "quais mesas estão livres numa janela" é pergunta do domínio de reservas, não de mesas. Nenhum pacote define uma interface de repositório genérica estilo Spring Data; cada domínio define só os métodos que consome, e não presume que o outro pacote vá implementá-los.
- **`allocator.go` isolado de `handler.go` e de `repository.go`**: depende só das interfaces `TableFinder`/`ReservationCreator` e dos erros sentinela de domínio (`errors.go`) — nunca importa `net/http` nem `pgx`/`pgconn`. Tradução de erro de driver para erro de domínio acontece exclusivamente em `repository.go`. É o que viabiliza testar validação e roteamento de erro sem banco (Fase 1b) e concorrência real com banco (Fase 1c) sem duplicar lógica.
- **`httpserver/` separado**: mantém `main.go` fino, facilita testar router/middleware isoladamente.
- **Sem pasta `service/`**: regra de negócio mora no pacote de domínio, não numa camada intermediária genérica.
- **Nome do pacote Go `table`** (diretório) é independente do nome da tabela SQL `restaurant_tables` — sem colisão real, mas `restauranttable` ou `venue` são alternativas se parecer confuso na prática.

---

## 7. Infraestrutura

### Stack de infra decidida

| Item | Escolha | Motivo |
|---|---|---|
| Migração de schema | `golang-migrate` | Padrão de fato, SQL puro, `up`/`down` |
| Driver Postgres | `pgx/v5` | `lib/pq` está em modo manutenção; `pgx` expõe `pgconn.PgError.Code` nativamente, necessário para o retry da Fase 1c |
| Documentação de API | `swaggo/swag` + `swaggo/http-swagger` | Gera OpenAPI a partir de anotações nos handlers, servido em `/swagger/index.html` |
| Containerização | Nenhuma por enquanto | Postgres já é remoto (Supabase); `go run ./cmd/api` é suficiente |

### Conexão com o Postgres — correção sobre versão anterior

A versão anterior desta spec cravava conexão direta (porta 5432, `db.[PROJECT_REF].supabase.co`) e descartava o connection pooler citando restrição a prepared statements nomeados. O raciocínio técnico estava certo, mas incompleto: existem **três** modos de conexão no Supabase, não dois.

- **Direct connection** (porta 5432, `db.[PROJECT_REF].supabase.co`): hoje, em projetos novos, só resolve por padrão via **IPv6** — suporte IPv4 nessa rota virou add-on pago. Se a rede local não tiver saída IPv6, a conexão simplesmente não resolve, e o erro não deixa isso óbvio (timeout de DNS/conexão, não uma mensagem de "IPv6 necessário").
- **Transaction pooler / Supavisor** (porta 6543): IPv4, mas em modo transaction não sustenta prepared statements nomeados entre chamadas — é o motivo original (correto) para descartar essa opção.
- **Session pooler** (porta 5432, host `aws-x-<region>.pooler.supabase.com`): IPv4, e por operar em modo *session* mantém prepared statements nomeados normalmente — não tem a restrição que motivou descartar o transaction pooler.

**Decisão corrigida: usar o session pooler.** Resolve o problema de IPv4 sem reintroduzir o problema de prepared statements que já havia sido corretamente identificado como razão para evitar o transaction pooler.

### Variáveis de ambiente

```bash
# .env.example
DATABASE_URL=postgresql://postgres.[PROJECT_REF]:[PASSWORD]@aws-x-[REGION].pooler.supabase.com:5432/postgres?sslmode=require
PORT=8080
CORS_ALLOWED_ORIGIN=http://localhost:5173
ENV=development

# Horário de funcionamento do restaurante — usado por GET /tables/{id}/availability (seção 3)
# para calcular janelas livres dentro de limites reais, e não 00:00–23:59
SERVICE_START=18:00
SERVICE_END=23:00
SERVICE_TZ=America/Sao_Paulo
```

`sslmode=require` é obrigatório em qualquer um dos modos de conexão do Supabase — sem esse parâmetro a conexão falha com erro que não deixa óbvio que a causa é SSL.

`SERVICE_START`/`SERVICE_END`/`SERVICE_TZ` resolvem a lacuna do endpoint de disponibilidade (seção 3): sem horário de funcionamento definido em algum lugar do sistema, "janelas livres no dia" não tem limites para calcular, e `?date=` sobre uma coluna `timestamptz` não tem como definir "o dia" sem um timezone de referência. Ficam como configuração global do restaurante (não por mesa) nesta fase — consistente com a premissa de restaurante único da seção 1.

### CORS

Middleware manual liberando apenas `CORS_ALLOWED_ORIGIN` (nunca `*`), mesmo em projeto de estudo.

### Swagger

Anotações (`@Summary`, `@Param`, `@Success`, etc.) são escritas diretamente nos handlers desde a primeira versão — não é retrabalho posterior. `swag init` aponta para `cmd/api/main.go` e gera `docs/docs.go` + `docs/swagger.json`.

### Frontend (fora do escopo desta spec de backend)

Vue 3 (SPA) consumindo a API via REST. API deve ter CORS configurado para a origem do dev server Vue (`http://localhost:5173` por padrão via Vite).

---

## 8. Decisões registradas como débito técnico intencional

Para não serem confundidas com esquecimento durante a implementação:

1. **Erro sem `code` machine-readable** — v1 usa só string humana em `error`; migração futura é breaking change.
2. **Sem combinação de mesas** — desperdício de capacidade em alguns cenários é aceito; resolver exige redesenho de modelo.
3. **`maxRetries = 3` arbitrário** — pode causar livelock sob contenção extrema, não tratado nesta fase.
4. **Sem autenticação** — staff assumido confiável; toda a API é aberta nesta fase.
5. **Sem validação de formato de telefone** — `customer_phone` é texto livre na v1.

---

## 9. Changelog de revisão técnica (pré-implementação)

Antes de qualquer código ser escrito, esta spec passou por revisão técnica que identificou 5 problemas reais (dos quais 2 impediam a migration de sequer compilar) e 3 menores. Registrado aqui para rastreabilidade — nenhuma dessas correções foi feita silenciosamente:

1. **Constraint de overlap não compilava** (seção 2): `tsrange` trocado por `tstzrange`, único que aceita `timestamptz` sem cast implícito. Semântica `[)` do range documentada como decisão, não acidente.
2. **`AllocateTable` tinha duas assinaturas incompatíveis** (seções 4 e 5): unificado para depender de interfaces `TableFinder`/`ReservationCreator` definidas pelo próprio pacote consumidor, em vez de `*sql.DB` concreto — restaura o objetivo de testabilidade sem banco na Fase 1b.
3. **Retry cego no caminho manual** (seção 5): separado `ErrNoAvailability` (heurística automática esgotada) de `ErrTableUnavailable` (mesa específica pedida está ocupada); caminho manual não retenta, pois não há "próxima opção" a reconsultar.
4. **`GET /availability` sem horário de funcionamento definido** (seções 3 e 7): adicionado `SERVICE_START`/`SERVICE_END`/`SERVICE_TZ` como configuração global do restaurante.
5. **Conexão direta ao Supabase dependia de IPv6** (seção 7): trocado para session pooler (porta 5432 via `pooler.supabase.com`), que resolve IPv4 sem reintroduzir a restrição de prepared statements do transaction pooler.
6. **Menores**: `CHECK` de valores válidos em `status` adicionado; `Clock` injetável especificado para a validação de "não pode ser no passado"; `WITH SCHEMA extensions` adicionado à criação da extensão `btree_gist` (convenção Supabase).

## 10. Changelog de revisão técnica — segunda rodada

A correção #2 do changelog anterior (interfaces em vez de `*sql.DB`) resolveu um problema e, ao ser aplicada sem revisão completa das consequências, deixou três novos abertos — todos identificados antes de qualquer código ser escrito:

1. **`allocator.go` continuava importando o driver Postgres, agora indiretamente**: `isExclusionViolation` inspecionava `pgconn.PgError` de dentro do allocator. Corrigido com tradução de erro na fronteira — `reservation/repository.go` é o único arquivo do pacote `reservation` que importa `pgx`/`pgconn`; ele traduz `23P01` para o erro sentinela `ErrSlotTaken` antes de devolver. O mesmo princípio vale para `table/repository.go` (`23505` → `table.ErrDuplicateName`).
2. **As interfaces não sustentavam o contrato do `POST /reservations`**: `Insert` agora devolve `(Reservation, error)` (necessário porque o `id` é gerado pelo banco); a função de alocação foi renomeada de `AllocateTable` para `CreateReservation` e devolve `(Reservation, error)` (reflete que ela insere, não só escolhe mesa); `GetByID` foi adicionado a `TableFinder` (necessário para validar a mesa pedida no caminho manual).
3. **O objetivo declarado da Fase 1b estava impreciso**: "testar a heurística sem banco" praticamente não sobra como lógica testável, já que a seleção (`ORDER BY capacity ASC LIMIT 1`) roda no SQL. Objetivo reescrito (seção 4) para focar em validações e roteamento de erro/retry — que são o que de fato tem substância para teste de unidade nesta fase.
4. **Menores**: comentários desatualizados na árvore de pastas corrigidos; validação #8 adicionada (seção 3) para que `starts_at` respeite `SERVICE_START`/`SERVICE_END` — sem isso, reservas fora do expediente eram aceitas silenciosamente e nunca refletidas pelo endpoint `/availability`, gerando inconsistência observável entre banco e UI. `ends_at` pode ultrapassar `SERVICE_END` (decisão confirmada: última reserva do dia).

## 11. Changelog de revisão técnica — terceira rodada

Identificado antes de tocar em `table/repository.go` (segundo arquivo da ordem de implementação), evitando que o erro fosse escrito em código:

1. **Ambiguidade sobre quem implementa `TableFinder`** (seções 4 e 6): o nome e o tipo de retorno (`[]Table`) sugeriam `table.PostgresRepo` como implementação natural, mas `FindCandidates` exige `JOIN` com `reservations` para filtrar overlap — se `table/repository.go` implementasse isso, o pacote `table` passaria a conhecer `reservations`, contradizendo a organização por domínio já declarada na seção 6. Corrigido: `table/repository.go` fica só com CRUD de `restaurant_tables`; `reservation/repository.go` implementa `TableFinder` **e** `ReservationCreator` num único struct, usando `table.Table` como tipo de retorno (import de `reservation` → `table`, sem ciclo). A relação entre os pacotes deixou de ser simétrica por design, não por descuido.
2. **Árvore de pastas desatualizada** (seção 6): `table/repository.go` não define interface (nunca definiu — isso já estava certo), mas a rodada anterior ainda o descrevia como implementação de `TableFinder`, o que a correção #1 desfaz.
3. **"Transação básica" como objetivo de aprendizado da 1a não correspondia a nenhum caso do desenho atual** (seção 1): com `INSERT` único protegido pela constraint `EXCLUDE`, não há multi-statement a coordenar. Removido do objetivo declarado da 1a — mesmo padrão de correção já aplicado ao objetivo da 1b (seção 4): quando o objetivo listado não corresponde ao que o código exercita, corrige-se o objetivo, não se inventa um caso artificial só para justificá-lo.
