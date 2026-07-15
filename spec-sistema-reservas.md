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
- ~~**Combinação/partição de mesas**~~ — **revisto na Fase 3a (seção 14).** O item original juntava **três problemas diferentes**, e só um valia a pena:
  - **Combinação** (juntar mesas para um grupo grande): **construída** na Fase 3a, no caminho manual. O sistema estava dando `409` para grupos que o restaurante consegue sentar — um falso negativo, não uma otimização.
  - **Partição** (dividir uma mesa de 8 entre dois grupos de 4 diferentes): **não será construída.** Não é limitação técnica: sentar desconhecidos juntos não é otimização de capacidade, é reclamação. O negócio não quer.
  - **Combinação automática** (o sistema escolher quais mesas juntar): **não será construída** — Fase 3b, seção 14. Exige um grafo de adjacência que ninguém quer manter e uma busca NP-difícil, para automatizar uma decisão que o maître toma melhor olhando o salão.
- **Replanejamento de reservas já confirmadas** — remanejar quem já reservou para reduzir desperdício. É o que de fato resolveria o exemplo de desperdício da seção 4, e continua fora de escopo.
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
| **2** | **Outbox transacional** + worker pool para notificação assíncrona | goroutines, channels, `context` (timeout **e cancelamento**), transação multi-statement, fila em Postgres com `FOR UPDATE SKIP LOCKED` |
| **3a** | Combinação de mesas **no caminho manual**: o staff informa `table_ids`, o sistema registra e protege cada mesa | **Migração de constraint crítica com dados vivos** (expand/contract), tabela de junção, desnormalização para viabilizar a `EXCLUDE`, trigger para invariante estrutural, mudança quebrada de contrato |
| **3b (mapeada, NÃO construída)** | Combinação **automática** — o sistema escolhe quais mesas juntar | Grafo de adjacência + busca NP-difícil. Ver seção 14: o custo é alto, o valor é baixo, e o maître decide melhor. |

### Estado da implementação

**1a, 1b e 1c estão concluídas e verificadas.** A `EXCLUDE` foi provada contra o banco real (sobreposição bloqueada, limites `[)` confirmados, cancelamento liberando o horário); a heurística e o roteamento de erro têm testes sem banco; a concorrência foi validada com `go test -race -count=20`, 10 goroutines disputando a mesma mesa, sempre com exatamente um vencedor.

Além do escopo previsto, entraram três coisas que a spec original não tinha e que se mostraram necessárias: **RLS** (seção 7), **testes de contrato contra o `swagger.json`** (seção 7) e o pacote **`httpx`** (seção 6). As três estão documentadas nas suas seções e no changelog da quarta rodada (seção 12).

**A Fase 2 está concluída** (seção 13). Ela mudou de escopo em relação ao plano original: um worker pool alimentado por um channel em memória **perde notificação** em qualquer crash ou restart, e a razão de existir de uma notificação é chegar. Foi construída como **outbox transacional** — o que preserva integralmente o objetivo de aprendizado (goroutines, channels, `context`) e ainda adiciona um caso real de transação multi-statement, que o projeto até então não tinha.

**A Fase 3a está concluída** (seção 14): combinação de mesas no caminho manual, com a `EXCLUDE` migrada de `reservations` para uma tabela de junção via **expand/contract**, sem o banco ficar desprotegido em nenhum instante. A **Fase 3b** (combinação automática) está **mapeada e deliberadamente não construída** — o custo é alto, o valor é baixo, e a decisão é melhor tomada por um humano olhando o salão.

**~~O backend está completo.~~** — a frase estava errada, e a seção 15 registra por quê. Ele estava completo *como backend*: ao começar o frontend, descobriu-se que a API não sabia responder duas perguntas que a **primeira tela** faz (*"que horas o restaurante abre?"* e *"quais mesas estão livres às 20h?"*), e que resolvê-las no cliente significava duplicar config e reimplementar em JavaScript a única lógica de domínio real do projeto. Corrigido com `GET /service-hours` e `GET /availability?date=` (seções 3 e 15).

**A lição fica registrada:** *"o backend está completo"* é uma frase que **só o consumidor pode dizer** — e esta spec a escreveu sem ter um.

**Agora sim o backend está completo**, e desta vez com um consumidor para provar. O que resta é o frontend Vue.

**Atualização — config editável (seção 16).** O frontend Vue foi construído, e a primeira coisa que o staff quis não foi *ler* o expediente, mas *editá-lo* — e `.env` não é editável por quem não tem shell no servidor. Isso tirou o horário do ambiente e o pôs no banco (migration `0009`), editável em runtime, com **dias de funcionamento** e **exceções por data** — conceitos que esta spec não previa. A **seção 16** conta o porquê; as seções 3, 6, 7 e 15 ganharam notas apontando para lá. É o quarto *drift* documentado do projeto, e o segundo da própria spec (o primeiro foi a seção 12).

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
| `PATCH` | `/reservations/{id}` | Remarca: cancela a atual e cria uma nova (ID **novo**) numa transação só. Não estava na spec original — ver nota abaixo |
| `DELETE` | `/reservations/{id}` | Cancela (soft delete → `status = cancelled`) |
| `GET` | `/tables/{id}/availability?date=` | Janelas livres da mesa no dia, dentro do horário de funcionamento (ver seção 7 — `SERVICE_START`/`SERVICE_END`/`TZ`) |

> **`PATCH /reservations/{id}` (remarcação) entrou depois da spec original** e é feature à parte da config editável: editar uma reserva não muta a linha — cancela a antiga e insere uma nova numa transação (interface `ReservationReplacer` no allocator), emitindo **um** evento `reservation_updated` no lugar do par cancelamento+confirmação (migration `0008`). Está listada aqui para a tabela não mentir; o *porquê* completo não foi redigido nesta rodada (ver nota de escopo ao final da seção 16).

### Restaurante (adicionados na seção 15, para o frontend)

| Método | Rota | Descrição |
|---|---|---|
| `GET` | `/service-hours` | Expediente, fuso, **dias de funcionamento** e **exceções por data** do restaurante. Existe para o frontend **não guardar uma cópia** de `SERVICE_START`/`SERVICE_END`/`SERVICE_TZ` |
| `PUT` | `/service-hours` | **Altera** o expediente e os dias de funcionamento (config editável — seção 16) |
| `POST` | `/service-exceptions` | Marca uma data como fechada/aberta, fugindo da regra semanal (upsert) |
| `DELETE` | `/service-exceptions/{day}` | Remove a exceção: a data volta a seguir a regra semanal (idempotente) |
| `GET` | `/availability?date=` | **Grade do dia**: cada mesa ativa e suas janelas livres. É o que a tela de combinação (Fase 3a) precisa para o staff escolher quais mesas juntar |

**O `GET /service-hours` e o `/availability?date=` não estavam na spec original e não são capricho de UI** — sem eles, o frontend precisaria duplicar config do backend e reimplementar o *sweep* do `availability.go` em JavaScript. O racional completo está na **seção 15**.

**O `PUT /service-hours`, o `POST` e o `DELETE` de exceções entraram ainda depois** (config editável em runtime): o `GET` deixou de ser um retrato do `.env` no boot e virou leitura da tabela `restaurant_settings`, editável pelas três rotas novas. A resposta ganhou `open_weekdays` e `exceptions`. O *porquê* está na **seção 16**.

### Validações em camada de aplicação (antes do banco)

A constraint SQL é a rede de segurança final contra race condition (resolvida de fato na 1c), não o lugar para mensagem de erro amigável.

Na criação de reserva:
1. `party_size > 0`
2. `starts_at < ends_at`
3. `starts_at` não pode ser no passado — **implementar via `Clock` injetável** (`interface { Now() time.Time }`), não `time.Now()` chamado direto dentro da função de validação. Sem isso, testes de unidade para este caso precisam de datas fixas no futuro que "expiram" sozinhas com o tempo, quebrando build meses depois sem relação com o código em si
4. Mesa existe e `is_active = true` (quando `table_id` for informado)
5. `party_size <= table.capacity`
6. ~~Checagem de overlap em app antes do `INSERT`~~ — **removida na quarta rodada de revisão (seção 12).** A justificativa original era que a constraint SQL "não é o lugar para mensagem de erro amigável". Mas ela deixou de ser o lugar do erro bruto: o `23P01` é traduzido em `ErrSlotTaken` no repositório, que o allocator converte em `ErrTableUnavailable` / `ErrNoAvailability`, que o handler transforma em `409` com mensagem legível. **O objetivo da validação está atendido por outro meio.** O `SELECT` prévio, não: ele só somaria um round-trip e uma janela de corrida, sem entregar nada que o caminho de erro já não entregue
7. Se a constraint do banco disparar (`23P01` — exclusion_violation), retornar `409 Conflict` com mensagem legível, nunca erro bruto do Postgres. **Este item, e não o #6, é o que garante a mensagem amigável**
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

**Quarta correção — o que a implementação de fato exigiu (seção 12):**

1. **`GetByID` virou `GetTable`.** A terceira correção mandou os dois contratos morarem no mesmo struct (`PostgresRepo`). Mas esse struct também serve o `GET /reservations/{id}`, que precisa de um `Get(ctx, id) (Reservation, error)`. **Dois métodos com o mesmo nome no mesmo tipo não compilam em Go.** `GetTable` também é um nome melhor: `GetByID` num repositório que lida com dois agregados não diz *id de quê*.

2. **`CreateReservation` virou método de um struct `Allocator`, não uma função livre.** A validação #3 exige um `Clock` injetável e a #8 exige o horário de funcionamento — a função livre viraria de seis parâmetros, e cada teste repetiria os quatro fixos. As dependências entram uma vez no construtor; o método recebe só o pedido.

3. **`ServiceHours` e `Clock` são declarados pelo pacote `reservation`, não importados de `config`.** Se o domínio importasse `config`, a regra "reserva só dentro do expediente" passaria a depender de como as variáveis de ambiente estão organizadas. O `main.go` é quem traduz `Config` → `ServiceHours`.

4. **`errors.go` tem quatro sentinelas e um tipo.** Sentinela responde "qual condição"; ela não consegue carregar *"Grupo de 6 pessoas excede a capacidade da mesa (4)"* — daí `ValidationError` ser um tipo. `errors.Is` para as sentinelas, `errors.As` para o tipo.

```go
// reservation/errors.go
var (
    ErrSlotTaken        = errors.New("horário já ocupado nessa mesa")        // sinal INTERNO repo→allocator
    ErrNoAvailability   = errors.New("nenhuma mesa disponível...")           // → 409
    ErrTableUnavailable = errors.New("a mesa solicitada já está reservada...") // → 409
    ErrNotFound         = errors.New("reserva não encontrada")               // → 404
)

type ValidationError struct{ Message string } // → 400, carrega os números do caso

// reservation/allocator.go — declara tudo que consome; não importa pgx nem net/http
type TableFinder interface {
    FindCandidates(ctx context.Context, partySize int, starts, ends time.Time) ([]table.Table, error)
    GetTable(ctx context.Context, id uuid.UUID) (table.Table, error)
}

type ReservationCreator interface {
    Insert(ctx context.Context, r Reservation) (Reservation, error)
}

type Clock interface{ Now() time.Time }

type ServiceHours struct {
    Start, End time.Duration // desde a meia-noite, no fuso TZ
    TZ         *time.Location
}

type Allocator struct { /* finder, creator, hours, clock */ }

func NewAllocator(TableFinder, ReservationCreator, ServiceHours, Clock) *Allocator
func (a *Allocator) CreateReservation(ctx context.Context, req AllocationRequest) (Reservation, error)
```

### Disponibilidade — `GET /tables/{id}/availability` (seção 3)

**A rota é de mesas; o código mora em `reservation`.** "Quais janelas desta mesa estão livres?" só se responde olhando `reservations` — é pergunta do domínio de reservas, exatamente como o `FindCandidates`. **A URL não é a fronteira do domínio.**

Vive em `reservation/availability.go`, num tipo `Schedule` separado do `Allocator` (agenda não é alocação), e contém **a única lógica de domínio real em Go do projeto**: um *sweep* com cursor que subtrai as janelas ocupadas do expediente, recortando as reservas que ultrapassam o fechamento (permitidas pela validação #8). É também o único algoritmo aqui que não daria para empurrar para o SQL sem sofrimento — e por isso o único com teste de unidade de lógica de verdade.

```go
type ScheduleReader interface {
    GetTable(ctx context.Context, id uuid.UUID) (table.Table, error)
    BusyWindows(ctx context.Context, tableID uuid.UUID, from, to time.Time) ([]Window, error)
}

func NewSchedule(ScheduleReader, ServiceHours) *Schedule
func (s *Schedule) FreeWindows(ctx context.Context, tableID uuid.UUID, dia string) ([]Window, error)
```

`ScheduleReader` **redeclara `GetTable`, que a `TableFinder` também tem — e isso não é duplicação.** São consumidores diferentes, cada um declarando o mínimo de que precisa; o `*PostgresRepo` satisfaz as duas sem saber que existem. Extrair uma superinterface comum acoplaria dois consumidores que não têm nada a ver um com o outro.

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
const maxRetries = 3

func (a *Allocator) CreateReservation(ctx context.Context, req AllocationRequest) (Reservation, error) {
    if err := a.validarPedido(req); err != nil { // validações 1,2,3,8 — sem I/O
        return Reservation{}, err
    }

    // Manual: SEM retry. A Mesa 12 vai colidir de novo na segunda tentativa e na
    // terceira — não existe "próxima opção" a encontrar.
    if req.PreferredTableID != nil {
        res, err := a.tryAllocateSpecific(ctx, req) // validações 4 e 5 acontecem aqui
        if errors.Is(err, ErrSlotTaken) {
            return Reservation{}, ErrTableUnavailable
        }
        return res, err
    }

    // Automático: retry faz sentido porque a RECONSULTA enxerga um mundo novo —
    // a mesa que acabou de ser tomada some das candidatas, e a próxima primeira
    // da fila é OUTRA mesa.
    for range maxRetries {
        res, err := a.tryAllocateAutomatic(ctx, req)
        if err == nil {
            return res, nil
        }
        if errors.Is(err, ErrSlotTaken) {
            continue
        }
        // Definitivo — inclusive ErrNoAvailability ("não existe mesa que sirva"),
        // que sai na PRIMEIRA volta em vez de custar três consultas idênticas.
        return Reservation{}, err
    }

    return Reservation{}, ErrNoAvailability
}
```

**Invariante:** `ErrSlotTaken` nunca escapa desta função. Manual: convertido. Automático: vira `continue`, ou o loop esgota e vira `ErrNoAvailability`. O handler tem uma cláusula explícita para o caso de ele vazar mesmo assim — **`500` com log de `INVARIANTE VIOLADO`, e não um `409` disfarçado**. Erro de programação não pode se passar por erro de usuário: se ele responder bonito, ninguém nunca conserta.

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
│       └── main.go               # monta dependências, sobe servidor, shutdown gracioso
├── internal/
│   ├── table/
│   │   ├── model.go              # Table, ErrNotFound, ErrDuplicateName
│   │   ├── repository.go         # Postgres, só CRUD de restaurant_tables — não conhece reservations
│   │   ├── handler.go            # handlers HTTP (/tables); declara a interface que consome
│   │   ├── handler_test.go       # contra fake, sem banco
│   │   └── contract_test.go      # valida as respostas contra o swagger.json
│   ├── reservation/
│   │   ├── model.go              # Reservation, AllocationRequest, Status
│   │   ├── errors.go             # 4 sentinelas + ValidationError (seção 4)
│   │   ├── repository.go         # implementa TableFinder, ReservationCreator, ScheduleReader
│   │   │                         # e ReservationReplacer (remarcação); único arquivo do
│   │   │                         # pacote que importa pgx/pgconn
│   │   ├── allocator.go          # Allocator.CreateReservation/Replace + validações + retry (1c);
│   │   │                         # declara ExpedienteVigente (config editável — seção 16)
│   │   ├── allocator_test.go     # validações e roteamento de erro, sem banco (1b)
│   │   ├── availability.go       # Schedule.FreeWindows + o sweep de janelas (seção 4)
│   │   ├── availability_test.go  # o sweep, sem banco
│   │   ├── integration_test.go   # concorrência contra Postgres real (1c); pulado sem TEST_DATABASE_URL
│   │   ├── handler.go            # handlers HTTP (/reservations, PATCH remarcação, /tables/{id}/availability, /availability)
│   │   └── contract_test.go      # valida as respostas contra o swagger.json
│   ├── settings/                 # config editável do restaurante — ver seção 16
│   │   ├── model.go              # Settings, Exception, AbertoEm (a única lógica de negócio do pacote)
│   │   ├── repository.go         # Postgres; satisfaz reservation.ExpedienteVigente; único a importar pgx
│   │   ├── handler.go            # handlers HTTP (GET/PUT /service-hours, /service-exceptions)
│   │   ├── model_test.go         # AbertoEm: precedência da exceção sobre a regra semanal, sem banco
│   │   └── contract_test.go      # valida as respostas contra o swagger.json
│   ├── notification/             # Fase 2 — ver seção 13
│   │   ├── outbox.go             # Enqueue(ctx, tx, ...) — grava o evento na transação de quem chama
│   │   ├── repository.go         # a fila: Claim (FOR UPDATE SKIP LOCKED), MarkSent, MarkFailed
│   │   ├── sender.go             # interface Sender + LogSender
│   │   ├── worker.go             # Dispatcher: poller + pool + shutdown gracioso
│   │   └── worker_test.go        # drenagem no shutdown; prova o context.WithoutCancel
│   ├── httpx/                    # PACOTE FOLHA — só stdlib. Ver "ciclo de import" abaixo
│   │   └── response.go           # httpx.JSON, httpx.Error, ErrorResponse
│   ├── openapitest/              # validador de contrato compartilhado (seção 7)
│   │   └── openapitest.go
│   ├── httpserver/
│   │   ├── router.go             # monta rotas, injeta handlers, aplica middlewares
│   │   └── middleware.go         # logging, recovery, CORS, MaxBytes
│   └── config/
│       └── config.go             # variáveis de ambiente, validadas no boot
├── migrations/
│   ├── 0001_create_tables.{up,down}.sql
│   ├── 0002_create_reservations.{up,down}.sql
│   ├── 0003_secure_schema_migrations.{up,down}.sql   # RLS — ver seção 7
│   ├── 0004_create_notifications.{up,down}.sql      # outbox — ver seção 13
│   ├── 0005_notifications_claim.{up,down}.sql       # 'sending' + visibility timeout
│   ├── 0006_reservation_tables_expand.{up,down}.sql # Fase 3a EXPAND — ver seção 14
│   ├── 0007_reservation_tables_contract.{up,down}.sql # Fase 3a CONTRACT
│   ├── 0008_notification_kind_updated.{up,down}.sql # 'reservation_updated' no CHECK (remarcação)
│   └── 0009_restaurant_settings.{up,down}.sql       # expediente editável + exceções — ver seção 16
├── docs/                          # gerado por swaggo — VAI VERSIONADO (ver seção 7)
├── go.mod
├── go.sum
└── .env.example
```

### O ciclo de import que a versão anterior desta spec criava

A versão anterior punha os helpers de JSON em `httpserver/response.go`. Mas `httpserver/router.go` **importa** `table` e `reservation` para montar as rotas. Se os pacotes de domínio importassem `httpserver` para usar os helpers:

```
httpserver ──importa──> table, reservation
table      ──importa──> httpserver          ← ciclo. Go recusa compilar.
```

Não é questão de estilo: é erro de compilação. A correção é o pacote **folha** `internal/httpx`, que não importa nada interno e por isso pode ser importado por todos — inclusive pelo próprio `httpserver`.

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

> **Correção (seção 16):** desde a migration `0009`, estas três variáveis **deixaram de ser a fonte da verdade**. O expediente passou a viver na tabela `restaurant_settings`, editável em runtime, e o `.env` só as usa como **semente** da primeira linha, no boot. Editar o horário pela API não toca o `.env`; e o allocator/agenda passaram a ler o expediente do banco a cada operação, não do ambiente. Ver seção 16.

### Row Level Security — a porta que o Supabase abre sem avisar

**Descoberto durante a implementação; não estava previsto.** O débito técnico #4 ("sem autenticação, toda a API é aberta") se refere à *sua* API em Go. Mas o Supabase **expõe automaticamente todo o schema `public` via PostgREST**, com a chave `anon` — que é pública por design e vai no bundle do Vue. Sem fazer nada, qualquer pessoa com a URL do projeto consegue `DELETE` em `restaurant_tables` pelo endpoint REST do próprio Supabase, **contornando o backend inteiro**. Não é o "sem auth" que a spec aceitou: é uma segunda porta que ninguém sabia que estava aberta.

Fecha de graça, com `ENABLE ROW LEVEL SECURITY` e **zero policies**:

| Role | `BYPASSRLS` | Efeito |
|---|---|---|
| `postgres` (a API Go e o `golang-migrate`) | ✅ | não é afetada |
| `service_role` | ✅ | não é afetada — **esta chave nunca pode ir para o frontend** |
| `anon`, `authenticated` (PostgREST público) | ❌ | bloqueados |

Aplicado às três tabelas — inclusive `schema_migrations`, que o `golang-migrate` cria sozinho, no `public` e sem RLS (migration `0003`). Sem isso, alguém com a chave `anon` poderia reescrever a versão do schema.

### CORS

Middleware manual liberando apenas `CORS_ALLOWED_ORIGIN` (nunca `*`), mesmo em projeto de estudo. Com `Vary: Origin` — sem ele, um proxy pode cachear a resposta liberada para uma origem e entregá-la para outra.

### Swagger + teste de contrato

Anotações (`@Summary`, `@Param`, `@Success`, etc.) escritas direto nos handlers. `swag init -g cmd/api/main.go -o docs --parseInternal` gera `docs/`.

**O `docs/` vai versionado**, mesmo sendo código gerado: o `router.go` faz `import _ "reservas-restaurante/docs"`, então sem ele o projeto não compila num clone limpo.

**A crítica que a spec original não fazia:** documentação *code-first* é uma **narrativa sobre** o código, não uma **restrição sobre** ele. Nada garante que `@Success 201` seja verdade — se o handler passar a devolver `200`, a anotação continua lá, verde, mentindo, e o frontend confia.

A saída **não** é migrar para spec-first (`oapi-codegen`): o gerador escreveria o `decode`, a validação e a assinatura do handler, apagando as três coisas que este projeto existe para ensinar. **Spec-first é melhor engenharia e pior pedagogia.**

A saída adotada é o **teste de contrato** (`internal/openapitest`): carrega o `swagger.json` gerado, bate nos handlers reais com `httptest`, e falha se **(a)** o status devolvido não estiver declarado na anotação, ou **(b)** o corpo não bater com o schema. Recupera a garantia do spec-first sem abrir mão de escrever o handler à mão. Verificado que morde: trocar o `201` do `POST /tables` por `200` deixa o teste vermelho.

#### Correção da Fase 3a: o teste era mais fraco do que esta spec afirmava

A Fase 3a renomeou `table_id` para `table_ids` na resposta do `POST /reservations`. **Esta spec previa que o teste de contrato ficaria vermelho. Ele não ficou.**

O motivo é que **o JSON Schema é permissivo nas duas pontas**: o swaggo não emite `required` nem `additionalProperties: false`, então (1) a **ausência** de `table_id` é aceita, e (2) o `table_ids` **novo** entra como propriedade extra. A resposta nova validou contra o schema velho, e o teste ficou verde enquanto o contrato estava quebrado.

**Conclusão desconfortável: validar contra o schema pega mudança de *tipo*, mas não pega *renomeação de campo* — que é a quebra de contrato mais comum que existe.**

Corrigido com `exigirCamposExatos`, que é **deliberadamente mais estrito que o OpenAPI**: exige igualdade exata entre as chaves do corpo e as propriedades do schema, nas **duas** direções. Um campo na resposta que não está no swagger falha; um campo no swagger que a resposta não traz também falha. Isso só é legítimo porque nesta API toda resposta traz todos os seus campos sempre — num contrato com campos opcionais, a segunda checagem estaria errada.

Verificado que morde: contra o `swagger.json` desatualizado, ele acusa **as duas** pontas do rename.

`openapitest` é um pacote normal e não um `_test.go` porque **arquivos de teste não são importáveis entre pacotes**, e tanto `table` quanto `reservation` precisam do mesmo validador. O custo é honesto: um pacote que só testes usam fica na árvore de build.

### Testes de integração e o `-race`

O teste de concorrência (1c) é **pulado** se `TEST_DATABASE_URL` não estiver definida — variável **separada** da `DATABASE_URL` de propósito, para que `go test ./...` nunca escreva em produção por reuso acidental.

**No Windows, `go test -race` exige um compilador C** (`CGO_ENABLED=1` + gcc, ex: `scoop install gcc`). Sem isso o comando falha com `-race requires cgo`, e a Fase 1c não pode ser verificada.

### Frontend

Vue 3 (SPA) consumindo a API via REST. API deve ter CORS configurado para a origem do dev server Vue (`http://localhost:5173` por padrão via Vite).

**Esta seção dizia "fora do escopo desta spec de backend", e a frase custou dois endpoints.** Ao começar o frontend descobriu-se que a API não sabia responder duas perguntas que qualquer tela precisa fazer — e que a saída fácil (resolver no cliente) significava duplicar config e reimplementar lógica de domínio em JavaScript. Corrigido na **seção 15**.

O `SERVICE_START`/`SERVICE_END`/`SERVICE_TZ` **nunca vai para o `.env` do Vue**: o frontend lê de `GET /service-hours`. Config duplicada entre dois processos é a mesma classe de mentira que o teste de contrato existe para impedir na documentação — só que sem ninguém checando.

---

## 8. Decisões registradas como débito técnico intencional

Para não serem confundidas com esquecimento durante a implementação:

1. **Erro sem `code` machine-readable** — v1 usa só string humana em `error`; migração futura é breaking change.
2. **Sem combinação de mesas** — desperdício de capacidade em alguns cenários é aceito; resolver exige redesenho de modelo.
3. **`maxRetries = 3` arbitrário** — pode causar livelock sob contenção extrema, não tratado nesta fase.
4. **Sem autenticação** — staff assumido confiável; toda a API em Go é aberta nesta fase. **Isto não vale para o PostgREST do Supabase**, que foi fechado com RLS (seção 7) — lá a exposição seria involuntária, não uma decisão.
5. **Sem validação de formato de telefone** — `customer_phone` é texto livre na v1.
6. **Retry sem backoff** — três tentativas imediatas, sem espera nem jitter. Para o volume de um restaurante é irrelevante; num teste de estresse com dezenas de goroutines, elas martelam o banco em rajada.
7. **`PATCH /tables/{id}` é last-write-wins** — dois `PATCH` concorrentes na mesma mesa se sobrescrevem em silêncio. Não há `@Version`/optimistic locking. A concorrência que este projeto trata é a de *reservas* (via `EXCLUDE`), não a de edição de mesas. **Relacionado:** a checagem de "não desativar mesa com reserva" (seção 15) é um *check-then-act* com a mesma natureza — janela de corrida aceita, sem constraint no banco para ampará-la.
8. **Nenhuma validação de duração máxima de reserva** — nada impede uma reserva de 12 horas.
9. **Entrega `at-least-once`, não `exactly-once`** (Fase 2). Se o `Send` funciona e o `MarkSent` falha, a notificação é enviada de novo quando o visibility timeout devolver a linha. **Isto não tem conserto:** `exactly-once` exigiria commit atômico entre o Postgres e o provedor de SMS — um 2PC entre sistemas que não se conhecem. Todo sistema de fila honesto entrega `at-least-once` e empurra a idempotência para o consumidor final.
10. **Fila por polling, não por `LISTEN/NOTIFY`** — a notificação sai com até `PollInterval` (2s) de atraso, e o banco é consultado mesmo quando não há nada a fazer. O `LISTEN/NOTIFY` do Postgres eliminaria a espera, ao custo de uma conexão dedicada e de um caminho de reconexão a manter.
11. ~~**Nada observa o estado `failed`**~~ — **PARCIALMENTE RESOLVIDO.** Uma notificação que esgota as tentativas vira `failed` com o erro gravado. Agora existe `GET /notifications` (default `?status=failed`) que lista as que falharam — a falha silenciosa passou a ter um lugar onde aparece. **O que NÃO foi feito, e continua débito:** é observação *pull* (alguém tem que abrir o endpoint), não um *alerta* que empurra. Alerta de verdade exigiria um canal (e-mail/Slack) que não existe — o único `Sender` é o `LogSender` (item #12). Fechar o resto depende de fechar o #12 primeiro.
12. **`LogSender` é o único `Sender`** — notificação de verdade (e-mail/SMS) está fora de escopo desde a seção 1. Trocá-lo por um cliente de Twilio é implementar **uma** função.
13. **Capacidade de uma combinação = soma das capacidades** (Fase 3a). É uma simplificação: duas mesas de 4 encostadas às vezes sentam 8, às vezes 6 (você perde os lugares das pontas que ficaram no meio), às vezes 10 (cabe gente nas quinas). Restaurante real tem regra própria, e ela não sai de uma fórmula. **A soma é o guarda-corpo, não a verdade** — ela impede um grupo de 20 em duas mesas de 4, e é só o que se pede dela.
14. **Nada valida adjacência física** (Fase 3a). O sistema aceita combinar a `Mesa 01` com a `Mesa 08` mesmo que estejam em salões opostos. É consequência direta de não construir a 3b: sem grafo de adjacência, não há o que validar. **Quem garante que as mesas encostam é o humano que as escolheu** — o que é aceitável exatamente porque a combinação é manual.
15. **O caminho automático não combina** (Fase 3a). Um grupo de 10 sem `table_ids` informado ainda recebe `409`, mesmo com 6+4 livres. Combinar automaticamente é a Fase 3b, deliberadamente não construída (seção 14).
16. **Tipos do domínio escritos à mão em TypeScript**, não gerados do `swagger.json` (seção 15). Gerar acoplaria o build do frontend ao `swag init` para 4 tipos — custa mais do que resolve. **É o mesmo *drift* de sempre, na terceira encarnação**: código → doc (resolvido pelo teste de contrato) → spec (não resolvido, ver seção 12) → agora TS. Aceito porque o teste de contrato protege o lado que importa — o servidor —, mas **nada impede o TS de divergir do Go**, e o compilador do Vue vai concordar alegremente com um campo que não existe mais.
17. ~~**`POST /service-exceptions` não checa reservas ao fechar uma data**~~ — **RESOLVIDO.** Era o irmão do `PUT /service-hours` (seção 16): marcar `2026-12-25` como `is_open=false` fechava o dia por cima de reservas confirmed, que ficavam de pé enquanto a agenda passava a mostrar o dia como fechado. O `SaveException` agora, quando `is_open=false`, consulta `ContarReservasNoDia` antes do upsert e devolve `409` se houver reserva (`500` se a contagem falhar); `is_open=true` (abertura especial) nunca consulta — só adiciona disponibilidade, como reativar mesa. É o mesmo padrão da checagem do `PUT` e da desativação de mesa, adaptado a "o dia inteiro fecha" em vez de "a janela aperta". **Ver seção 16.**
18. ~~**`r.serviceTZ` do `reservation.PostgresRepo` é fixo no boot**~~ — **RESOLVIDO.** Vinha do `cfg.ServiceTZ` e ficava *stale* se o staff editasse o fuso pelo `PUT /service-hours`. As três queries que precisavam do fuso (`ContarReservasFuturas` da mesa, `ContarReservasNoDia` da exceção, o filtro do `?date=`) passaram a lê-lo do banco a cada chamada, via `tzVigenteSQL` — `(SELECT service_tz FROM restaurant_settings WHERE id = 1)` embutido no `AT TIME ZONE`. **O preço, registrado:** `reservation` agora lê `restaurant_settings` (tabela do domínio `settings`) em SQL — o grafo de imports do Go continua limpo (reservation não importa settings), mas há um acoplamento no nível do banco. O campo `serviceTZ` e o parâmetro `serviceTZ` do `NewPostgresRepo` **foram removidos** (num ajuste pós-revisão): sem ninguém lendo o fuso do boot, mantê-los era código morto. `cfg.ServiceTZ` passou a ser validado-mas-não-consumido em Go, o mesmo status que `cfg.ServiceStart`/`ServiceEnd` já tinham — guarda fail-fast do env, não dado vivo. **Nota de verificação:** existe agora um teste de integração (`TestQueriesComFusoDoBanco`) que exercita as três queries contra um Postgres real, provando que a subquery `AT TIME ZONE (SELECT service_tz …)` é SQL válido contra o schema. Ele **só roda com `TEST_DATABASE_URL`** — ao escrevê-lo, dois helpers de teste stale desde a Fase 3a foram descobertos e corrigidos (referenciavam `reservations.table_id`, coluna removida pela 0007). A execução final contra o banco fica com o autor; até lá, a verificação é *escrita mas não rodada*.
19. ~~**`DELETE /service-exceptions/{day}` de uma abertura especial re-fecha o dia sem checar reservas**~~ — **RESOLVIDO.** Era a terceira porta do mesmo gap (`PUT /service-hours`, seção 16, e `POST`, item #17). Se o staff abriu com exceção (`is_open=true`) uma segunda-feira normalmente fechada, criou reservas naquele dia, e depois **apagava** a exceção, o dia voltava à regra semanal (fechado) e as reservas ficavam órfãs. O `DeleteException` agora, **só quando o dia da semana está fora de `open_weekdays`** (ou seja, quando remover a exceção fecha o dia), consulta `ContarReservasNoDia` e devolve `409` se houver reserva. Se a regra semanal já abre o dia, remover no máximo re-abre — e re-abrir nunca consulta a agenda, mesma família de "só a operação que fecha é barrada" da mesa, do PUT e do POST.

> **Fechado:** o débito "`reservation/handler_test.go` não existe" foi resolvido. O arquivo cobre o parsing dos filtros (formato do `?date=`, enum do `?status=`, UUID do `?table_id=`), o `DisallowUnknownFields`, a conversão DTO→domínio (`table_id` ausente/null/informado), e — o mais importante — **o invariante do `ErrSlotTaken`**: se ele vazar do allocator, o handler devolve `500` com log de `INVARIANTE VIOLADO`, nunca um `409` disfarçado. Verificado que o teste falha ao mapear `ErrSlotTaken` para `409`, denunciando tanto o status errado quanto o vazamento da mensagem interna no corpo.

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

---

## 12. Changelog de revisão técnica — quarta rodada (pós-implementação)

As três rodadas anteriores foram feitas **antes** de qualquer código. Esta é a primeira **depois**: com as fases 1a, 1b e 1c implementadas e verificadas, a spec havia divergido do código em sete pontos — e um documento de referência que mente é pior do que documento nenhum, porque alguém decide errado confiando nele.

A ironia é registrada de propósito: este projeto construiu um **teste de contrato** justamente para impedir a documentação da API de mentir sobre os handlers. Um nível acima, a própria spec virou o documento desatualizado — o mesmo *drift*, só que sem ninguém checando.

### Divergências corrigidas

1. **Ciclo de import na estrutura de pastas** (seção 6): o layout original (`httpserver/response.go`) **não compilava** — `httpserver` importa os pacotes de domínio, então eles não podem importar `httpserver` de volta. Criado o pacote folha `internal/httpx`.

2. **`TableFinder.GetByID` → `GetTable`** (seção 4): colidia com o `Get(ctx, id) (Reservation, error)` que o mesmo `PostgresRepo` precisa expor para o `GET /reservations/{id}`. Dois métodos homônimos no mesmo tipo não compilam.

3. **`CreateReservation` virou método de `*Allocator`** (seções 4 e 5): a função livre precisaria de seis parâmetros depois que o `Clock` (validação #3) e o `ServiceHours` (validação #8) entraram.

4. **`errors.go` tem quatro sentinelas e um tipo** (seção 4): faltavam `ErrNotFound` e o `ValidationError` — sentinela não carrega os números do caso concreto, e o formato de erro da seção 3 exige que carregue.

5. **Validação #6 removida** (seção 3): o objetivo dela (mensagem amigável em vez de erro bruto do Postgres) já é atendido pela tradução `23P01` → `ErrSlotTaken` → `ErrTableUnavailable`. O `SELECT` prévio só somaria round-trip e janela de corrida.

6. **RLS não estava previsto e era necessário** (seção 7): o Supabase expõe o schema `public` via PostgREST com a chave `anon`, que é pública. O débito #4 ("sem auth") era uma decisão sobre a API em Go; a exposição do PostgREST teria sido um acidente.

7. **Estrutura de pastas incompleta** (seção 6): faltavam `availability.go`, `openapitest/`, os testes de contrato e de integração, e a migration `0003`.

### Adições que a spec original não previa

- **Teste de contrato contra o `swagger.json`** (seção 7) — sem ele, a doc *code-first* pode mentir indefinidamente.
- **`GET /tables/{id}/availability` implementado em `reservation/`**, não em `table/` (seção 4) — a URL não é a fronteira do domínio.
- **Quatro débitos técnicos novos** (seção 8, itens 6 a 9), incluindo a lacuna do `reservation/handler_test.go`.

---

## 13. Fase 2 — Outbox transacional e worker pool

### Por que a spec original estava errada

A spec pedia *"worker pool para notificação assíncrona pós-reserva"*, com o trabalho chegando por um channel alimentado pelo handler. **Isso perde notificação.** A reserva é confirmada, o e-mail é enfileirado em memória, o processo cai — deploy, OOM, `Ctrl+C` — e a notificação evapora. O cliente tem a reserva no banco e nunca soube. Não é um detalhe de robustez: **a razão de existir de uma notificação é chegar.**

A correção é o **transactional outbox**: a intenção de notificar é gravada como uma linha, **na mesma transação** que muda o estado da reserva. Ou existem as duas, ou nenhuma. Um worker consome a tabela. Se o processo morre, a linha continua lá.

**E não se perde nada do aprendizado:** o worker pool continua existindo — goroutines, channels, `context`, shutdown gracioso. Só muda **de onde o trabalho vem**.

### Modelo

`notifications` (migrations `0004` e `0005`):

| Coluna | Papel |
|---|---|
| `reservation_id`, `kind` | o evento (`reservation_confirmed` \| `reservation_cancelled`) |
| `payload jsonb` | **snapshot** do que enviar, congelado no instante do evento |
| `status` | `pending` → `sending` → `sent` \| `failed` |
| `attempts`, `last_error` | máquina de retry, no banco |
| `claimed_at` | **visibility timeout** |

**`payload` e não um `JOIN` com `reservations`.** Se o worker fizesse `JOIN` na hora de enviar, uma notificação de *"confirmada"* despachada com atraso poderia sair descrevendo uma reserva **que já foi cancelada**. O outbox carrega **o fato como ele foi**, não como ele está — é um log de eventos, não uma view do estado.

**Toda fila persistente tem três estados, não dois.** A `0004` modelou só a escrita e esqueceu o consumo; a `0005` corrigiu. O worker precisa **reivindicar** a linha antes de enviar (segurar uma transação de banco aberta durante uma chamada HTTP externa é inaceitável num pool), e se ele morrer entre reivindicar e enviar, o `claimed_at` faz a fila **devolver** a linha. Quem modela só `pending → sent` está assumindo que o consumidor nunca morre no meio.

### A fila: `FOR UPDATE SKIP LOCKED`

**A spec descartou o `SELECT FOR UPDATE` para reservas (seção 5), e estava certa. Aqui ele é a ferramenta correta — e a diferença não é a primitiva, é o que se está travando.**

- Em reservas, *"a Mesa 5 entre 19h e 21h"* **não é uma linha**: é uma **condição sobre linhas que ainda não existem**. Não há o que travar, e o lock pessimista travaria a mesa inteira, gerando contenção entre horários que nem se sobrepõem.
- Aqui, *"a notificação 42"* **é uma linha** — discreta, existente. É exatamente o que um lock de linha protege.

O **`SKIP LOCKED`** faz o segundo worker **pular** o que o primeiro já reivindicou, em vez de esperar por ele. É o que transforma N workers em paralelismo real em vez de uma fila serializada com passos extras.

`attempts` é incrementado **na reivindicação, não na falha**. Se só subisse ao falhar, uma mensagem venenosa — que *mata* o worker em vez de retornar erro — seria reivindicada, mataria o processo, voltaria pelo timeout, mataria o próximo, para sempre.

### O pool: `close(channel)` e `context.WithoutCancel`

**`close(fila)` é o encerramento inteiro.** Os workers estão em `for n := range fila`, e um channel fechado **que ainda tem itens continua entregando** — o `range` só termina quando ele esvazia. Fechar o channel diz, de uma vez: *"não vem trabalho novo, mas termine o que já está aí"*. Sem channel de `done`, sem contador, sem sinalizar worker por worker.

**`context.WithoutCancel` é a linha que separa um pool que funciona de um que corrompe dados.** No shutdown, o `ctx` do dispatcher **já está cancelado** — mas os workers ainda estão drenando. Se usassem esse `ctx` para falar com o banco, **o `MarkSent` falharia sempre**, e toda notificação enviada durante o encerramento seria reenviada no próximo boot (o visibility timeout a devolveria à fila). **Cancelamento serve para parar de começar coisas novas, não para impedir de terminar as que já começaram.**

O `worker_test.go` prova isso: trocar `WithoutCancel` por `ctx` deixa a asserção de `ctx.Err()` vermelha. *Comentário não fica vermelho quando alguém o contraria.*

**Backpressure sai de graça do buffer.** O channel tem o tamanho de um lote: o poller não consegue reivindicar um segundo lote enquanto o primeiro não for consumido — ele fica parado no `fila <- n`. A memória nunca cresce sem limite, o banco não é consultado mais rápido do que os workers enviam, e **nada é descartado**. As alternativas ruins seriam descartar quando cheio (perde notificação) ou buffer infinito (OOM).

### O bug que o outbox revelou no `DELETE /reservations/{id}`

O cancelamento sempre foi idempotente — `204` nas duas vezes — e nunca foi problema. **No instante em que passou a produzir um evento, "não fazer nada" e "fazer de novo" deixaram de ser equivalentes**: o cliente receberia dois SMS de cancelamento, atrás de dois status codes perfeitamente corretos.

Corrigido com `WHERE id = $1 AND status = 'confirmed'`, que **não é um `if` — é um compare-and-swap atômico.** Sob dois cancelamentos concorrentes, o Postgres serializa pelo lock da linha; o segundo `UPDATE` reavalia o `WHERE` depois do commit do primeiro, vê que o status já mudou, e afeta zero linhas. Sem lock explícito, sem `@Version`, sem retry.

> **Idempotência do endpoint não é idempotência do efeito colateral.**

### Ordem de encerramento

1. **`srv.Shutdown`** — o servidor HTTP para de aceitar requisições e drena as em voo. Uma reserva sendo criada agora ainda enfileira sua notificação, na transação dela.
2. **`pararDispatcher()`** — só então o pool é derrubado, drenando o que já pegou.

Invertida, a ordem **atrasaria** notificações, mas **não as perderia** — a linha fica `pending` e o próximo boot a pega. É o outbox pagando de novo: uma decisão que num pool em memória seria de **corretude** virou uma decisão de **latência**.

---

## 14. Fase 3a — Combinação de mesas (manual)

### O item de escopo original juntava três problemas

A spec listava *"combinação/partição de mesas"* como um item só, e dava como exemplo *"mesa de 8 com grupo de 4 não pode ceder os 4 lugares restantes"*. Isso são **três coisas**:

| | O que é | Veredito |
|---|---|---|
| **Combinação** | Juntar mesas para um grupo grande | **Construída** — é a 3a |
| **Partição** | Dividir uma mesa entre dois grupos diferentes | **Não construída.** Não é limitação técnica: sentar desconhecidos juntos não é otimização, é reclamação |
| **Replanejamento** | Remanejar reservas já confirmadas | Fora de escopo, e é o único que resolveria o exemplo de desperdício da seção 4 |

### Por que a combinação vale a pena

Salão com mesas de `2,2,4,4,4,6,6,8`. Liga um grupo de **10**. O sistema respondia:

> `409` — *"nenhuma mesa disponível para o horário solicitado"*

**Isso é mentira.** A casa senta 10: empurra a de 6 na de 4. O sistema estava **recusando reservas que o restaurante aceitaria** — um falso negativo, não um desperdício de otimização. E são justamente as reservas de **maior valor**: recusar um grupo de 12 dói muito mais que recusar um de 2.

### Por que a 3b (automática) NÃO foi construída

**Combinar mesas não é um problema de software — é um problema que um humano já resolve melhor.** O maître sabe que há uma coluna entre a Mesa 3 e a Mesa 4, que grupo grande perto da cozinha é reclamação, e que a mesa da janela está guardada para um aniversário. **Nada disso cabe num grafo de adjacência.**

Três razões concretas:

1. **O grafo de adjacência é dado que ninguém quer manter**, e que apodrece em silêncio quando o salão é rearranjado. Quando apodrece, o sistema sugere combinações fisicamente impossíveis, e o staff **para de confiar no sistema inteiro** — inclusive nas partes que funcionam.
2. **A busca é NP-difícil** na forma geral (parente de *bin packing* e *set cover*), e a heurística seria sobrescrita pelo staff metade das vezes.
3. **O sistema não precisa decidir a combinação.** Ele precisa **parar de dizer não** e **registrar o que o humano decidiu.**

### O modelo

```sql
reservation_tables (reservation_id, table_id, starts_at, ends_at, status)
```

**As três colunas desnormalizadas não são preguiça — são a única forma de a garantia existir.** Uma `EXCLUDE` opera sobre **uma linha**: precisa do `table_id` e do intervalo lado a lado. **Constraint não faz `JOIN`.** Sem elas, a não-sobreposição não poderia ser expressa no banco, viraria checagem de aplicação, e a race condition da Fase 1c voltaria.

**E um trigger mantém a sincronia — contrariando tudo que esta spec defende sobre mágica invisível.** O critério que justifica a exceção:

> **Invariante que precisa valer independentemente de quem escreve mora no banco.** Regra de negócio que só a aplicação aplica mora na aplicação.

Sincronizar na aplicação significa que **um** `UPDATE` esquecido — numa migration futura, num `psql` manual, num endpoint de remarcação escrito daqui a seis meses — deixa a `EXCLUDE` protegendo dados velhos, **em silêncio**. Trigger para lógica de negócio é veneno; trigger para invariante estrutural é a ferramenta certa.

### A migração: expand / contract

**O núcleo técnico do projeto — a `EXCLUDE` — teve que ser reconstruído em outra tabela, com dados vivos.** Foi feito em duas migrations, e essa separação é o aprendizado principal da fase:

- **`0006` (expand):** cria a junção, faz o backfill, monta a **nova** `EXCLUDE`, cria o trigger, e torna `table_id` nullable. **Não derruba nada.** A ordem `backfill → CREATE CONSTRAINT` é a rede de segurança: se o backfill estiver errado, o `ADD CONSTRAINT` falha, a migration reverte, e a constraint antiga continua no lugar.
- **Entre as duas:** troca do código, reinício, e **verificação contra o banco real** — a nova constraint bloqueia sobreposição (`23P01`), o trigger sincroniza o cancelamento, a disponibilidade enxerga a combinação.
- **`0007` (contract):** só então derruba a constraint antiga e a coluna `table_id`.

**Em nenhum instante o banco fica sem proteção contra overbooking.** É a diferença entre *"migrei o schema"* e *"migrei o schema sem apagar a luz no meio do caminho"*.

Os `down` das duas falham de propósito se existir reserva combinada (subquery escalar estourando com *"more than one row"*). Poderiam "funcionar" com um `LIMIT 1`, escolhendo uma mesa e descartando a outra **em silêncio** — e um `down` que perde dado sem avisar é pior que um que não roda.

### O que o compilador provou sobre a arquitetura

`TableID uuid.UUID` → `TableIDs []uuid.UUID` produziu **6 erros de compilação, em 3 arquivos, num único pacote**. `table/`, `notification/`, `httpserver/`, `config/` e `main.go` **não apareceram** — não por cuidado, mas porque **não podem** saber que `TableID` existe.

**O compilador virou o checklist de migração.** Não houve `grep` seguido de torcida.

E o **contrato mudou de forma quebrada** (`table_id` → `table_ids`) de propósito **agora**: o frontend Vue ainda não existe, e este é o momento mais barato que vai existir. Depois dele, o custo seria duplo.

### Os três significados de `table_ids`

```
vazio/ausente/null  → heurística automática + retry (Fase 1b/1c)
uma mesa            → override manual, sem retry
duas ou mais        → COMBINAÇÃO, sem retry
```

Sem ponteiro, ao contrário de todos os outros campos opcionais do projeto: **slice já tem "ausente" embutido** (`nil`, e `len(nil) == 0`). O `*bool` precisou de ponteiro porque `false` é um valor legítimo. A regra: **ponteiro quando o zero value do tipo é indistinguível de um valor real; não quando não é.**

### A validação de duplicata, que não é óbvia

`table_ids: [A, A]` produziria duas linhas de junção com a mesma chave primária — um `23505` feio — **e, pior, contaria a capacidade da mesa A duas vezes**, deixando um grupo de 8 "caber" numa mesa de 4 informada em duplicata. **Erro de digitação virando overbooking.**

---

## 15. Preparação da API para o frontend

> Escrita **ao começar o frontend**, e não antes. É o registro de um erro de planejamento desta spec, não de uma fase prevista.

### O que a spec dizia sobre o frontend, e por que isso saiu caro

Três linhas, na seção 7: *"Vue 3 (SPA) consumindo a API via REST"*, mais o CORS. Nada além disso — e a seção 1 registrava, com alívio, que **"o backend está completo"**.

**Ele não estava.** Ele estava completo *como backend*. Nenhuma das perguntas abaixo tinha resposta, e as duas são perguntas que a **primeira tela** faz:

| A pergunta | O que a API respondia | O que o frontend faria sem o endpoint |
|---|---|---|
| *"Que horas o restaurante abre?"* | nada | `SERVICE_START=18:00` no `.env` do Vue — **a mesma verdade em dois processos**, e nenhum teste vigiando a divergência |
| *"Quais mesas estão livres às 20h?"* | só por mesa, e por dia | ou **N requisições** (uma por mesa, a cada mexida no horário), ou **reimplementar o `janelasLivres` em JavaScript** |

A segunda linha é a grave. O `janelasLivres` é, nas palavras da própria seção 4, **"a única lógica de domínio real em Go do projeto"** — e o desenho da API estava empurrando uma cópia dela para o cliente. Não por decisão: por omissão.

**A lição não é "faltaram dois endpoints".** É que *"o backend está completo"* é uma frase que **só o consumidor pode dizer**, e esta spec a escreveu sem ter um.

### `GET /service-hours` — e por que não `/config`

```json
{"start": "18:00", "end": "23:00", "tz": "America/Sao_Paulo"}
```

O nome estreito é a defesa. **Um endpoint chamado `/config` é um convite**: daqui a seis meses alguém precisa de mais um campo no frontend, vê um balde chamado *config*, e joga lá dentro — e um dia o balde vaza algo que ninguém devia ver. Em `/service-hours` não cabe outra coisa sem ficar óbvio que está errado.

A resposta é montada **uma vez, no boot**, e capturada pela closure do handler. Ela não pode mudar entre requisições — o `config.Load()` garante isso —, então recalculá-la a cada chamada fingiria uma dinâmica que não existe.

> **Correção (seção 16):** as duas frases acima deixaram de valer. Desde a migration `0009`, o expediente é **editável em runtime**: a resposta é relida do banco a cada chamada (não mais montada no boot), ganhou `open_weekdays` e `exceptions`, e existe um `PUT /service-hours` que a altera. *A dinâmica que não existia* passou a existir no instante em que editar o horário virou uma tela — e o argumento certo se inverteu: agora **é** recalcular a cada chamada que diz a verdade. A ironia é a de sempre neste projeto: a defesa de uma decisão vira o diagnóstico do que a substituiu.

### `GET /availability?date=` — a grade do dia

```json
[{"table_id": "…", "table_name": "Mesa 04", "capacity": 4,
  "free_windows": [{"starts_at": "…", "ends_at": "…"}]}]
```

**Duas** idas ao banco para o salão inteiro, não N: `ListActiveTables` + `BusyWindowsAll` (uma query cada). O `DayGrid` **não recalcula janela nenhuma** — chama o *mesmo* `janelasLivres`, por mesa, sobre dados que já chegaram.

E é isso que mantém o sweep num lugar só: com a grade na mão, *"quem está livre das 20h às 22h?"* é uma **checagem de contenção de intervalo** no cliente — um `.filter()`, não um algoritmo.

**O `ORDER BY table_id, starts_at` do `busyWindowsAllSQL` é correção, não cosmética.** O `janelasLivres` é um sweep com cursor e **exige** as ocupadas ordenadas por início — a garantia precisa valer *dentro de cada mesa*. Um `ORDER BY table_id` sozinho compila, roda, e devolve janelas livres erradas **em silêncio**.

### A URL segue a pergunta, não a tabela

A seção 4 usou o princípio *"a URL não é a fronteira do domínio"* para justificar `GET /tables/{id}/availability` **morar em `reservation/`**. Aqui o mesmo princípio aponta para o lado oposto, e as duas conclusões estão certas:

- `/tables/{id}/availability` → o sujeito é **uma mesa**. `{id}` no caminho.
- `/availability?date=` → o sujeito é **o dia**. A mesa é o *resultado*, não o recurso. Não há `{id}` para pendurar, e enfiá-la em `/tables/availability` diria que a pergunta é sobre a coleção de mesas quando ela é sobre a agenda.

### Redundância admitida

`/tables/{id}/availability` e `/availability` respondem a mesma pergunta em granularidades diferentes. **O antigo não foi apagado** — é contrato público (seção 3) e tem semântica que o novo não tem: mesa **inativa** devolve `[]`, enquanto a grade simplesmente não a lista. Mas fica registrado: se o frontend nunca o chamar, é candidato a morrer.

### O que o compilador provou, de novo

Adicionar `DayGrid` à interface `schedule` do handler **quebrou o `fakeSchedule` dos testes** — e o `go build ./...` passou. Ou seja: o código de produção estava íntegro, e a única coisa desatualizada era o dublê. O compilador apontou para ele, e para mais nada.

**Um `Mockito.mock()` teria ficado verde**, gerando o método novo sozinho, devolvendo `null`, e ignorando a capacidade nova em silêncio. Fake escrito à mão é uma struct que implementa uma interface de verdade: **método novo na interface é erro de compilação, sem exceção.** É o segundo dividendo da mesma decisão de arquitetura (seção 14, `TableID` → `TableIDs`).

### O buraco que a primeira tela revelou

**Dava para desativar uma mesa com reserva confirmada em cima.** O `PATCH /tables/{id}` não olhava reservas: a mesa sumia da agenda, e a reserva continuava de pé, `confirmed`, apontando para uma mesa que o sistema considerava fora de operação. O cliente aparecia às 20h, a mesa não estava no quadro, e ninguém tinha sido avisado.

**O bug sempre esteve lá.** Construir o botão "Desativar" não o criou — só o colocou a um clique de distância de um humano com um mouse. É a segunda vez nesta seção que o consumidor prova algo que o backend sozinho não tinha como provar.

#### Onde a checagem mora, e por que não no banco

O critério é o mesmo que justificou o trigger da Fase 3a, aplicado ao contrário:

> **Invariante que precisa valer independentemente de quem escreve mora no banco. Regra de negócio que só a aplicação aplica mora na aplicação.**

Uma migration futura pode legitimamente precisar desativar uma mesa. Isto é **regra de negócio**, não invariante estrutural — e portanto não vira `CHECK` nem trigger.

#### A interface atravessa a fronteira no sentido contrário

`table` não pode importar `reservation` — é a assimetria que sustenta a organização por domínio (seção 6). Mas a restrição *vem* de reservas.

A saída é o padrão que o projeto já usa três vezes, agora invertido: **`table` declara a interface de que precisa**, sem nomear o outro pacote.

```go
// table/handler.go — e não há import de `reservation` aqui
type agenda interface {
    ContarReservasFuturas(ctx context.Context, tableID uuid.UUID) (int, error)
}
```

O `*reservation.PostgresRepo` a satisfaz **sem nunca ter ouvido falar dela**. O acoplamento é real, e mora num único argumento de construtor no `main.go`, onde dá para vê-lo. Em Spring seria um `@Autowired` num campo, e a dependência entre os dois domínios ficaria invisível até alguém rodar o grafo de beans.

#### Três decisões dentro da checagem

1. **`ends_at > now()`, e não `starts_at > now()`.** A reserva que está **acontecendo agora** — entrou às 19h, sai às 21h, e são 20h — é a mais grave de todas. Filtrar por `starts_at` deixaria passar exatamente o caso em que há gente sentada.
2. **Só a desativação é barrada.** Renomear e mudar capacidade continuam livres: não fazem a mesa sumir de lugar nenhum. Barrá-las transformaria uma proteção em burocracia. **Reativar nunca consulta a agenda** — pôr uma mesa de volta em operação não pode quebrar nada, e uma checagem ali tornaria impossível desfazer uma desativação feita por engano.
3. **Falha ao contar vira `500`, nunca `409`.** Um erro de banco na contagem significa *"não sei se pode"*, e responder *"não pode"* seria mentir com confiança.

#### A corrida que fica, e é aceita

É um *check-then-act*: alguém pode criar uma reserva entre a contagem e o `UPDATE`. Ao contrário do overbooking, **esta corrida não tem constraint no banco para ampará-la** — e a spec não vai fingir que tem. É uma janela de milissegundos numa operação que o staff faz uma vez por semestre, contra um dano recuperável (reativar a mesa). Aceito, e registrado aqui em vez de escondido.

### Débito técnico novo

Item **16** da seção 8: tipos do domínio escritos à mão em TypeScript, não gerados do `swagger.json`.

---

## 16. Expediente editável em runtime — a config que saiu do `.env` e foi para o banco

> Escrita depois da seção 15. A seção 15 criou o `GET /service-hours` para o frontend **ler** o expediente. Esta seção é o que aconteceu quando a primeira coisa que o staff quis foi **editá-lo** — e `.env` não é editável por quem não tem shell no servidor.

### A premissa da seção 15 tinha prazo de validade

A seção 15 montava a resposta do `/service-hours` **uma vez, no boot**, capturada pela closure do handler, e registrava isso como virtude: *"recalculá-la a cada chamada fingiria uma dinâmica que não existe"*. A dinâmica não existia porque a fonte era o `.env`, e `.env` só muda com redeploy.

**No instante em que "mudar o horário" virou uma tela, a dinâmica passou a existir** — e a premissa da seção 15 virou o que ela combatia: uma verdade congelada que o sistema afirma ser fixa enquanto o usuário espera mudá-la. `SERVICE_START` no `.env` tem exatamente o mesmo defeito que a seção 15 apontou no `.env` do Vue: config que só um deploy altera, para um dado que o negócio muda sozinho.

A correção move o expediente para o banco (migration `0009`), rebaixando o `.env` a **semente inicial**: `config.Load` ainda lê `SERVICE_START/END/TZ`, mas só para popular a primeira linha da tabela; a fonte da verdade passa a ser `restaurant_settings`.

### `restaurant_settings` é um singleton, e o banco garante isso

```sql
id smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1)
```

Restaurante único (seção 1) → uma linha de config. O `CHECK (id = 1)` é o idioma para *"esta tabela tem no máximo uma linha"*: um segundo `INSERT` bate no PK e nunca existe uma "segunda config fantasma" competindo pela verdade. É o mesmo princípio das outras constraints do projeto — a invariante mora no banco, não numa convenção que a aplicação lembra de respeitar.

### Dias de funcionamento: um array, não sete colunas

`open_weekdays smallint[]`, na convenção `EXTRACT(DOW)` do Postgres (0=domingo … 6=sábado) — que por sorte é a **mesma** do `time.Weekday` do Go, então não há tabela de conversão entre banco e código a manter sincronizada. A pergunta que o domínio faz é *"o dia D está aberto?"*, que em array é `d = ANY(open_weekdays)` e em Go é um `map[time.Weekday]bool` indexado direto. Sete colunas booleanas exigiriam um `switch` para responder a mesma pergunta.

Uma `CHECK (open_weekdays <@ ARRAY[0,1,2,3,4,5,6])` impede um `{9}` de entrar: sem ela, um dia inexistente nunca casaria com `dow` nenhum e **fecharia o restaurante em silêncio** — a mesma classe de bug que a validação #8 (seção 3) evita do outro lado.

### Exceções, e a precedência que importa

`service_exceptions (day PK, is_open, note)`: datas que fogem da regra semanal. Fechar num dia normalmente aberto (feriado) ou abrir num normalmente fechado (véspera especial), e `is_open` diz qual. A decisão de design real está na **precedência**:

> A exceção vence a regra semanal.

*"Fechamos às segundas, mas nesta segunda abrimos para um evento"* só se expressa se a exceção do dia **ganhar** do `open_weekdays`. É o que `AbertoEm` faz — o único pedaço de lógica de negócio de verdade do pacote `settings`, e por isso o único com teste de unidade: consulta a exceção primeiro, e só cai na regra semanal quando não há exceção. É o mesmo comportamento de qualquer calendário.

O `day` é interpretado **no fuso do restaurante** (`time.ParseInLocation`), não em UTC: o dia da semana de um instante depende do fuso, e é o dia de parede do restaurante que decide se ele abre. É a mesma armadilha que a seção 15 já tratava no `diaAberto` do frontend, agora do lado do servidor — e as duas pontas usam a mesma convenção `0=domingo`, de propósito.

### Como a config chega ao domínio sem o domínio conhecer o pacote

O ponto não é **exibir** o expediente — é o allocator e a agenda **respeitarem** o que o staff editou. Uma UI que mostra "fechado" enquanto o backend aceita a reserva é a mentira silenciosa que o projeto inteiro combate (seção 15).

Antes, o `Allocator` recebia um `ServiceHours` **fixo no construtor**. Isso não sobrevive à edição: um valor capturado no boot ignora qualquer `UPDATE` posterior. A correção troca o valor por um **provedor**:

```go
// reservation/allocator.go — declarada pelo consumidor, como todas as outras
type ExpedienteVigente interface {
    HorasVigentes(ctx context.Context) (ServiceHours, error)
    AbertoEm(ctx context.Context, dia string) (bool, error)
}
```

O allocator pergunta o expediente vigente **a cada validação**, não uma vez na vida. E o padrão é o mesmo que a spec já usa quatro vezes (seções 4, 14, 15): **o consumidor declara a interface, o provedor a satisfaz sem ser nomeado.** O `settings.PostgresRepo` implementa `ExpedienteVigente`, e `reservation` **não importa `settings`** — não pode, seria ciclo, porque `settings` importa `reservation` para reusar o tipo `ServiceHours` (é o mesmo conceito de expediente; duplicá-lo criaria duas verdades). A dependência mora num argumento de construtor no `main.go`, onde dá para vê-la.

`AbertoEm` é método da interface, e não um `bool` pré-computado, pela mesma razão de `HorasVigentes`: a resposta depende de exceções que o staff pode ter gravado há um segundo.

### O `GET` deixou de ser closure; o `PUT` e as exceções são novos

A resposta do `/service-hours` **relê do banco a cada chamada** — o oposto do que a seção 15 fazia, e correto agora que o dado muda. O custo é um `SELECT` numa tabela de uma linha; um cache traria o problema de invalidá-lo quando o staff editar, e a config é lida raramente.

Rotas novas (seção 3):

| Método | Rota | Papel |
|---|---|---|
| `PUT` | `/service-hours` | Altera horário, fuso e dias de funcionamento |
| `POST` | `/service-exceptions` | Marca uma data como fechada/aberta (upsert: remarcar a mesma data corrige a decisão) |
| `DELETE` | `/service-exceptions/{day}` | A data volta a seguir a regra semanal (idempotente: apagar o que não existe devolve sucesso) |

Separar `PUT /service-hours` de `POST /service-exceptions` é deliberado: mudar o horário não é a mesma operação que adicionar um feriado, e juntá-las obrigaria o frontend a reenviar a lista inteira de exceções a cada ajuste de horário.

**As validações do handler espelham as constraints do banco** (`expediente_coerente`, `dias_validos`), mas existem para dar mensagem amigável (*"O fechamento deve ser depois da abertura."*), não erro bruto. É o mesmo princípio da validação de reserva antes da `EXCLUDE` (seção 3, #7): a constraint é a rede de segurança final, não o lugar da mensagem. Se uma constraint dispara **mesmo assim**, o handler trata como `500` — bug de app que deixou passar uma checagem —, não como `400`. Erro de programação não pode se passar por erro de usuário (seção 5).

### RLS, de novo e pelo mesmo motivo

As duas tabelas novas entram com `ENABLE ROW LEVEL SECURITY` e zero policies (seção 7). Sem isso, o PostgREST do Supabase exporia não só a **leitura** da config pela chave `anon` pública, mas a **edição** dela — qualquer um com a URL do projeto reescreveria o horário do restaurante contornando o backend inteiro. É exatamente o buraco da seção 7, e a correção é a mesma linha.

### O `down` que pode apagar sem medo

Ao contrário dos `down` da 0006/0007 (Fase 3a), que falham de propósito para não perder reserva combinada, o `down` da 0009 derruba as duas tabelas direto. **A diferença é o tipo do dado:** `restaurant_settings` e `service_exceptions` são **config**, não histórico. O expediente volta a ser responsabilidade do `.env` — para onde o código revertido volta a olhar. Perder a config editada é reversível (reconfigura-se em segundos); perder uma reserva não é. O critério de "quando um `down` pode destruir" é o mesmo da Fase 3a, aplicado ao contrário.

### O buraco que encolher o expediente abriu

**Dava para encolher o expediente por cima de uma reserva confirmed.** O `PUT /service-hours` fazia um `UPDATE` incondicional em `restaurant_settings`: apertar a janela (`18h–23h → 19h–22h`) ou tirar um dia de `open_weekdays` não olhava as reservas. A reserva das 20h de uma segunda continuava `confirmed` no banco, intocada — mas a partir dali o `AbertoEm` respondia `false` para aquele horário, e a agenda passava a mostrar como fechado exatamente o slot onde havia gente marcada. É o **mesmo buraco que a desativação de mesa revelou** (seção 15, "o buraco que a primeira tela revelou"), do outro lado: lá a mesa sumia com a reserva de pé; aqui o horário some com a reserva de pé.

#### O eixo da checagem é o `starts_at`, e isso não é escolha — é espelho

A pergunta "esta reserva ficaria fora do novo expediente?" tem que ter **exatamente a mesma resposta** que o `allocator.validarPedido` dá ao criar a reserva, senão o sistema passa a ter duas definições de "dentro do expediente". E o `validarPedido` (seção 3, validação #8) checa **só o início**: aceita a reserva se o dia de `starts_at` está aberto E a hora de `starts_at` cai em `[start, end)`. O término pode ultrapassar o fechamento de propósito — é a última mesa do dia, que senta às 22h30 e sai à meia-noite.

Logo a contagem do `PUT` é a **negação literal** desse aceite: dia fechado **OU** hora de início antes da abertura **OU** hora de início no/depois do fechamento. Checar `ends_at` teria parecido mais "seguro", mas inventaria uma regra que a criação de reserva não tem — e reprovaria no `PUT` uma reserva que o `POST` aceitaria alegremente. A checagem não pode ser mais rígida que a regra que ela protege.

#### Onde a checagem mora, e por que não no `Save`

No `settings.Handler.Update`, entre `montarSettings` e `repo.Save` — não dentro do `Save`. O `Save` continua um escritor sem opinião; quem sabe traduzir "há conflito" em `409` e "não consegui contar" em `500` é o handler. É a mesma divisão do `table.Handler` (seção 15): a checagem de negócio vive na borda HTTP, o repositório só persiste. Enfiar a contagem no `Save` obrigaria o repositório a devolver um erro tipado que o handler reinterpretaria — mais indireção para o mesmo efeito, e um `Save` que nenhum outro chamador poderia reusar sem herdar a regra.

#### A interface atravessa a fronteira settings → reservation

`settings` **não importa `reservation` por causa disto** — declara a pergunta e deixa o `main.go` costurar, como o `table` faz com a `agenda`:

```go
// settings/handler.go — a interface é declarada pelo consumidor
type agenda interface {
    ContarReservasForaDoExpediente(ctx context.Context, novo reservation.ServiceHours, diasAbertos []int) (int, string, error)
}
```

O `*reservation.PostgresRepo` a satisfaz sem nunca ter ouvido falar dela. (O `reservation.ServiceHours` no tipo não é ciclo: `settings` **já** importa `reservation` para reusar `ServiceHours` — é o mesmo conceito de expediente —, e `reservation` continua sem conhecer `settings`.) O acoplamento mora num argumento de construtor no `main.go`, o quinto uso da mesma struct de reservas, à vista.

#### Três decisões dentro da checagem, e uma que diverge da mesa

1. **Consulta `reservations`, não `reservation_tables`.** A `ContarReservasFuturas` da mesa lia a tabela de junção porque uma mesa pode estar ocupada como *metade* de uma combinação. Aqui a unidade é a **reserva** e seu único `starts_at` — a junção não agregaria nada, e contá-la duplicaria a reserva combinada.
2. **O fuso é o do expediente *candidato*, não o `r.serviceTZ` do boot.** O `PUT` pode estar justamente mudando o fuso; a pergunta é "sob o expediente **novo**, esta reserva fica fora?", então a hora de parede tem que ser lida no fuso novo. É onde esta checagem é *mais* correta que a da mesa, que ainda usa o `serviceTZ` fixo (débito #18, seção 8).
3. **`ends_at > now()`, e falha na contagem vira `500`.** Só reservas com efeito futuro/em curso importam — uma que já terminou fora do novo horário não faz mal a ninguém. E um erro de banco na contagem significa *"não sei se pode"*: responder `409` ("não pode") seria mentir com confiança, então é `500`. As duas decisões são idênticas às da mesa (seção 15), pelas mesmas razões.

#### A corrida que fica, e é aceita

É um *check-then-act*: alguém pode criar uma reserva entre a contagem e o `UPDATE`. Como na desativação de mesa, **esta corrida não tem constraint no banco para ampará-la** — e a spec não finge que tem. Janela de milissegundos numa operação que o staff faz raramente, contra dano recuperável (reeditar o expediente). Aceito e registrado, não escondido — mesmo raciocínio do débito #7 (seção 8).

#### O irmão que ficou de fora — e depois foi corrigido

O `POST /service-exceptions` tinha **o mesmo gap**: marcar uma data como fechada não checava reservas confirmed já existentes naquele dia. Ficou de fora da rodada do `PUT` de propósito, para aquela correção ser isolada e revisável — e foi corrigido logo em seguida, na sua própria rodada. O `SaveException` agora consulta `ContarReservasNoDia` antes de fechar uma data (`is_open=false`) e devolve `409` se houver reserva; abrir uma data (`is_open=true`) nunca consulta, porque só adiciona disponibilidade. A diferença em relação à checagem do `PUT`: fechar uma exceção fecha o **dia inteiro**, então é uma contagem por-dia — a forma da desativação de mesa, não a da janela de horas. Fechado como débito #17 (seção 8).

### Nota de escopo — o que esta rodada NÃO cobriu

A **remarcação de reserva** (`PATCH /reservations/{id}`, interface `ReservationReplacer`, evento `reservation_updated`, migration `0008`) entrou no código no mesmo período, mas é **feature independente** da config editável. Está registrada factualmente na seção 3 para a tabela de endpoints não mentir, porém **sem** a narrativa de *porquê* que as outras fases têm. Fica como o próximo trabalho de documentação, se e quando valer — anotado aqui em vez de fingido como completo, no mesmo espírito da seção 12: um documento de referência que finge cobertura é pior que um que admite o buraco.
