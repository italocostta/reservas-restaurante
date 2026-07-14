package reservation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	// Sem isto, LoadLocation falha no Windows DENTRO DESTE TESTE — o import em
	// branco do config não vale aqui: o binário de teste do pacote reservation
	// não linka o config.
	_ "time/tzdata"

	"github.com/google/uuid"

	"reservas-restaurante/internal/table"
)

// ---------- fakes: satisfazem TableFinder, ReservationCreator e Clock ----------

type fakeFinder struct {
	findFn func(ctx context.Context, partySize int, starts, ends time.Time) ([]table.Table, error)
	getFn  func(ctx context.Context, ids []uuid.UUID) ([]table.Table, error)

	findCalls int
	getCalls  int
}

func (f *fakeFinder) FindCandidates(ctx context.Context, partySize int, starts, ends time.Time) ([]table.Table, error) {
	f.findCalls++
	return f.findFn(ctx, partySize, starts, ends)
}

func (f *fakeFinder) GetTables(ctx context.Context, ids []uuid.UUID) ([]table.Table, error) {
	f.getCalls++
	return f.getFn(ctx, ids)
}

// devolve monta um getFn que sempre responde com as mesmas mesas.
func devolve(mesas ...table.Table) func(context.Context, []uuid.UUID) ([]table.Table, error) {
	return func(context.Context, []uuid.UUID) ([]table.Table, error) { return mesas, nil }
}

func ids(mesas ...table.Table) []uuid.UUID {
	out := make([]uuid.UUID, len(mesas))
	for i, m := range mesas {
		out[i] = m.ID
	}
	return out
}

type fakeCreator struct {
	insertFn func(ctx context.Context, r Reservation) (Reservation, error)
	calls    int
}

func (c *fakeCreator) Insert(ctx context.Context, r Reservation) (Reservation, error) {
	c.calls++
	return c.insertFn(ctx, r)
}

type fakeClock struct{ agora time.Time }

func (c fakeClock) Now() time.Time { return c.agora }

// ---------- cenário ----------

var fusoSP = mustLoadTZ("America/Sao_Paulo")

func mustLoadTZ(nome string) *time.Location {
	tz, err := time.LoadLocation(nome)
	if err != nil {
		panic(err)
	}
	return tz
}

// Sábado, 01/08/2026. "Agora" é meio-dia; a reserva é das 19h às 21h — dentro
// do expediente (18h–23h). Datas fixas, relógio fixo: este teste vai passar
// igual daqui a dez anos.
var (
	agora  = time.Date(2026, 8, 1, 12, 0, 0, 0, fusoSP)
	inicio = time.Date(2026, 8, 1, 19, 0, 0, 0, fusoSP)
	fim    = time.Date(2026, 8, 1, 21, 0, 0, 0, fusoSP)
)

func novoAllocator(f *fakeFinder, c *fakeCreator) *Allocator {
	return NewAllocator(f, c, ServiceHours{
		Start: 18 * time.Hour,
		End:   23 * time.Hour,
		TZ:    fusoSP,
	}, fakeClock{agora})
}

func pedido() AllocationRequest {
	return AllocationRequest{
		CustomerName:  "Maria Silva",
		CustomerPhone: "11999998888",
		PartySize:     2,
		StartsAt:      inicio,
		EndsAt:        fim,
	}
}

func mesa(nome string, capacidade int) table.Table {
	return table.Table{ID: uuid.New(), Name: nome, Capacity: capacidade, IsActive: true}
}

// ---------- validações: nenhuma pode tocar no repositório ----------

func TestValidacoesBarramAntesDoIO(t *testing.T) {
	casos := []struct {
		nome   string
		ajusta func(*AllocationRequest)
		trecho string // pedaço esperado da mensagem
	}{
		{"grupo zero", func(r *AllocationRequest) { r.PartySize = 0 }, "maior que zero"},
		{"nome vazio", func(r *AllocationRequest) { r.CustomerName = "   " }, "nome do cliente"},
		{"telefone vazio", func(r *AllocationRequest) { r.CustomerPhone = "" }, "telefone"},
		{"fim antes do início", func(r *AllocationRequest) { r.EndsAt = r.StartsAt.Add(-time.Hour) }, "posterior"},
		{"duração zero", func(r *AllocationRequest) { r.EndsAt = r.StartsAt }, "posterior"},
		{"no passado", func(r *AllocationRequest) {
			r.StartsAt = agora.Add(-time.Hour)
			r.EndsAt = agora.Add(time.Hour)
		}, "passado"},
		{"antes da abertura", func(r *AllocationRequest) {
			r.StartsAt = time.Date(2026, 8, 1, 17, 30, 0, 0, fusoSP)
			r.EndsAt = time.Date(2026, 8, 1, 19, 0, 0, 0, fusoSP)
		}, "fora do expediente"},
		{"depois do fechamento", func(r *AllocationRequest) {
			r.StartsAt = time.Date(2026, 8, 1, 23, 0, 0, 0, fusoSP)
			r.EndsAt = time.Date(2026, 8, 2, 1, 0, 0, 0, fusoSP)
		}, "fora do expediente"},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			finder := &fakeFinder{}
			creator := &fakeCreator{}

			req := pedido()
			tc.ajusta(&req)

			_, err := novoAllocator(finder, creator).CreateReservation(context.Background(), req)

			var ve ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("erro = %v (%T), quero ValidationError", err, err)
			}
			if !contem(ve.Message, tc.trecho) {
				t.Errorf("mensagem = %q, esperava conter %q", ve.Message, tc.trecho)
			}
			// O ponto do teste: validação inválida NÃO pode custar ida ao banco.
			if finder.findCalls+finder.getCalls+creator.calls != 0 {
				t.Errorf("repositório foi tocado (find=%d get=%d insert=%d) numa entrada inválida",
					finder.findCalls, finder.getCalls, creator.calls)
			}
		})
	}
}

// A última reserva do dia PODE terminar depois do fechamento — decisão explícita
// da validação #8. Senta às 22h30, sai à meia-noite.
func TestFimPodeUltrapassarOFechamento(t *testing.T) {
	m := mesa("Mesa A", 4)
	finder := &fakeFinder{findFn: func(context.Context, int, time.Time, time.Time) ([]table.Table, error) {
		return []table.Table{m}, nil
	}}
	creator := &fakeCreator{insertFn: func(_ context.Context, r Reservation) (Reservation, error) {
		return r, nil
	}}

	req := pedido()
	req.StartsAt = time.Date(2026, 8, 1, 22, 30, 0, 0, fusoSP)
	req.EndsAt = time.Date(2026, 8, 2, 0, 30, 0, 0, fusoSP) // já é o dia seguinte

	if _, err := novoAllocator(finder, creator).CreateReservation(context.Background(), req); err != nil {
		t.Fatalf("erro = %v, quero sucesso", err)
	}
}

// ---------- caminho manual ----------

// O TESTE CENTRAL da fase 1c. Se o caminho manual retentar, ele fica vermelho.
func TestManualNaoRetentaEDizAVerdade(t *testing.T) {
	m := mesa("Mesa 12", 4)

	finder := &fakeFinder{getFn: devolve(m)}
	creator := &fakeCreator{insertFn: func(context.Context, Reservation) (Reservation, error) {
		return Reservation{}, ErrSlotTaken // colide sempre
	}}

	req := pedido()
	req.PreferredTableIDs = ids(m)

	_, err := novoAllocator(finder, creator).CreateReservation(context.Background(), req)

	if !errors.Is(err, ErrTableUnavailable) {
		t.Fatalf("erro = %v, quero ErrTableUnavailable", err)
	}
	if errors.Is(err, ErrNoAvailability) {
		t.Error("devolveu ErrNoAvailability — mente sobre a causa: pode haver dez mesas livres, só não a pedida")
	}
	if creator.calls != 1 {
		t.Errorf("Insert chamado %d vez(es), quero exatamente 1 — não existe 'próxima mesa' a tentar", creator.calls)
	}
}

func TestManualValidaAsMesas(t *testing.T) {
	inativa := mesa("Mesa Fundos", 4)
	inativa.IsActive = false

	pequena := mesa("Mesa 2", 2)
	quatroA := mesa("Mesa A", 4)
	quatroB := mesa("Mesa B", 4)

	casos := []struct {
		nome       string
		pedidas    []uuid.UUID
		devolvidas []table.Table
		grupo      int
		trecho     string
	}{
		{
			// O repositório devolve as que achou; pedir 1 e receber 0 é o sinal.
			nome:    "mesa não existe",
			pedidas: []uuid.UUID{uuid.New()}, devolvidas: nil,
			grupo: 2, trecho: "não existem",
		},
		{
			nome:    "mesa inativa",
			pedidas: ids(inativa), devolvidas: []table.Table{inativa},
			grupo: 2, trecho: "inativa",
		},
		{
			nome:    "grupo excede a capacidade de uma mesa",
			pedidas: ids(pequena), devolvidas: []table.Table{pequena},
			grupo: 6, trecho: "excede a capacidade da mesa",
		},
		{
			// Fase 3a: a soma das duas é 8; um grupo de 10 não cabe.
			nome:    "grupo excede a capacidade COMBINADA",
			pedidas: ids(quatroA, quatroB), devolvidas: []table.Table{quatroA, quatroB},
			grupo: 10, trecho: "capacidade combinada",
		},
		{
			// Digitação repetida contaria a capacidade duas vezes: um grupo de 8
			// "caberia" numa mesa de 4 informada em duplicata. Erro de digitação
			// virando overbooking.
			nome:    "mesma mesa informada duas vezes",
			pedidas: []uuid.UUID{quatroA.ID, quatroA.ID}, devolvidas: []table.Table{quatroA},
			grupo: 8, trecho: "mais de uma vez",
		},
		{
			nome:       "pediu três mesas, uma não existe",
			pedidas:    []uuid.UUID{quatroA.ID, quatroB.ID, uuid.New()},
			devolvidas: []table.Table{quatroA, quatroB},
			grupo:      6, trecho: "não existem",
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			finder := &fakeFinder{getFn: devolve(tc.devolvidas...)}
			creator := &fakeCreator{}

			req := pedido()
			req.PreferredTableIDs = tc.pedidas
			req.PartySize = tc.grupo

			_, err := novoAllocator(finder, creator).CreateReservation(context.Background(), req)

			var ve ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("erro = %v (%T), quero ValidationError", err, err)
			}
			if !contem(ve.Message, tc.trecho) {
				t.Errorf("mensagem = %q, esperava conter %q", ve.Message, tc.trecho)
			}
			if creator.calls != 0 {
				t.Error("Insert foi chamado com uma mesa inválida")
			}
		})
	}
}

// ---------- combinação (Fase 3a) ----------

// A razão de existir da Fase 3a: um grupo de 8 num salão cuja maior mesa é 4.
// Antes, isso era um 409 mentiroso — o restaurante CONSEGUE sentar 8, empurrando
// duas mesas. O sistema recusava uma reserva que a casa aceitaria.
func TestCombinacaoAceitaGrupoMaiorQueQualquerMesa(t *testing.T) {
	a := mesa("Mesa A", 4)
	b := mesa("Mesa B", 4)

	finder := &fakeFinder{getFn: devolve(a, b)}

	var reservadas []uuid.UUID
	creator := &fakeCreator{insertFn: func(_ context.Context, r Reservation) (Reservation, error) {
		reservadas = r.TableIDs
		return r, nil
	}}

	req := pedido()
	req.PreferredTableIDs = ids(a, b)
	req.PartySize = 8 // não cabe em NENHUMA mesa sozinha

	if _, err := novoAllocator(finder, creator).CreateReservation(context.Background(), req); err != nil {
		t.Fatalf("erro = %v, quero sucesso — 4+4 comporta 8", err)
	}
	if len(reservadas) != 2 {
		t.Fatalf("reservou %d mesas, quero 2", len(reservadas))
	}

	// A heurística automática NÃO foi consultada: o staff já decidiu.
	if finder.findCalls != 0 {
		t.Errorf("FindCandidates chamado %d vezes numa combinação manual, quero 0", finder.findCalls)
	}
}

// Combinação é caminho MANUAL: colisão é definitiva, sem retry. As mesmas mesas
// vão colidir de novo na segunda tentativa.
func TestCombinacaoNaoRetenta(t *testing.T) {
	a := mesa("Mesa A", 4)
	b := mesa("Mesa B", 4)

	finder := &fakeFinder{getFn: devolve(a, b)}
	creator := &fakeCreator{insertFn: func(context.Context, Reservation) (Reservation, error) {
		return Reservation{}, ErrSlotTaken
	}}

	req := pedido()
	req.PreferredTableIDs = ids(a, b)
	req.PartySize = 8

	_, err := novoAllocator(finder, creator).CreateReservation(context.Background(), req)

	if !errors.Is(err, ErrTableUnavailable) {
		t.Fatalf("erro = %v, quero ErrTableUnavailable", err)
	}
	if creator.calls != 1 {
		t.Errorf("Insert chamado %d vez(es), quero 1 — combinação não retenta", creator.calls)
	}
}

// ---------- caminho automático ----------

// A heurística é candidatas[0] — e a ordem vem do ORDER BY capacity ASC do SQL.
// Este teste documenta esse contrato com o repositório.
func TestAutomaticoPegaAPrimeiraCandidata(t *testing.T) {
	menor := mesa("Mesa A", 4)
	maior := mesa("Mesa B", 8)

	finder := &fakeFinder{findFn: func(context.Context, int, time.Time, time.Time) ([]table.Table, error) {
		return []table.Table{menor, maior}, nil // já ordenadas, como o SQL entrega
	}}

	var reservadas []uuid.UUID
	creator := &fakeCreator{insertFn: func(_ context.Context, r Reservation) (Reservation, error) {
		reservadas = r.TableIDs
		return r, nil
	}}

	if _, err := novoAllocator(finder, creator).CreateReservation(context.Background(), pedido()); err != nil {
		t.Fatalf("erro = %v, quero sucesso", err)
	}
	// O caminho automático reserva UMA mesa. Combinar automaticamente é a Fase 3b,
	// deliberadamente não construída.
	if len(reservadas) != 1 {
		t.Fatalf("reservou %d mesas, quero 1 — o automático não combina", len(reservadas))
	}
	if reservadas[0] != menor.ID {
		t.Errorf("reservou a mesa errada — quero a menor suficiente (%s)", menor.Name)
	}
}

// Sem candidata nenhuma: ErrNoAvailability na PRIMEIRA volta, sem retry e sem
// tocar no Insert. Reconsultar não faria uma mesa aparecer.
func TestAutomaticoSemCandidatasNaoRetenta(t *testing.T) {
	finder := &fakeFinder{findFn: func(context.Context, int, time.Time, time.Time) ([]table.Table, error) {
		return []table.Table{}, nil
	}}
	creator := &fakeCreator{}

	_, err := novoAllocator(finder, creator).CreateReservation(context.Background(), pedido())

	if !errors.Is(err, ErrNoAvailability) {
		t.Fatalf("erro = %v, quero ErrNoAvailability", err)
	}
	if finder.findCalls != 1 {
		t.Errorf("FindCandidates chamado %d vezes, quero 1 — 'não há mesa' é definitivo", finder.findCalls)
	}
	if creator.calls != 0 {
		t.Error("Insert foi chamado sem candidata nenhuma")
	}
}

// O mecanismo do retry: a reconsulta enxerga um mundo novo. A mesa tomada some
// das candidatas, e a próxima primeira da fila é OUTRA mesa.
func TestAutomaticoReconsultaEAchaOutraMesa(t *testing.T) {
	tomada := mesa("Mesa A", 4)
	livre := mesa("Mesa B", 8)

	finder := &fakeFinder{}
	finder.findFn = func(context.Context, int, time.Time, time.Time) ([]table.Table, error) {
		if finder.findCalls == 1 { // primeira consulta
			return []table.Table{tomada, livre}, nil
		}
		return []table.Table{livre}, nil // alguém levou a Mesa A nesse meio-tempo
	}

	creator := &fakeCreator{}
	var reservada uuid.UUID
	creator.insertFn = func(_ context.Context, r Reservation) (Reservation, error) {
		if r.TableIDs[0] == tomada.ID {
			return Reservation{}, ErrSlotTaken
		}
		reservada = r.TableIDs[0]
		return r, nil
	}

	if _, err := novoAllocator(finder, creator).CreateReservation(context.Background(), pedido()); err != nil {
		t.Fatalf("erro = %v, quero sucesso na segunda tentativa", err)
	}
	if reservada != livre.ID {
		t.Error("deveria ter reservado a Mesa B na reconsulta")
	}
	if finder.findCalls != 2 || creator.calls != 2 {
		t.Errorf("find=%d insert=%d, quero 2 e 2", finder.findCalls, creator.calls)
	}
}

// Contenção extrema: colide sempre. Esgota exatamente maxRetries e devolve
// ErrNoAvailability — o livelock aceito na spec (débito #3).
func TestAutomaticoEsgotaOsRetries(t *testing.T) {
	m := mesa("Mesa A", 4)

	finder := &fakeFinder{findFn: func(context.Context, int, time.Time, time.Time) ([]table.Table, error) {
		return []table.Table{m}, nil
	}}
	creator := &fakeCreator{insertFn: func(context.Context, Reservation) (Reservation, error) {
		return Reservation{}, ErrSlotTaken
	}}

	_, err := novoAllocator(finder, creator).CreateReservation(context.Background(), pedido())

	if !errors.Is(err, ErrNoAvailability) {
		t.Fatalf("erro = %v, quero ErrNoAvailability", err)
	}
	if creator.calls != maxRetries {
		t.Errorf("Insert chamado %d vezes, quero exatamente maxRetries (%d)", creator.calls, maxRetries)
	}
	// O INVARIANTE: ErrSlotTaken é sinal interno e nunca pode chegar ao handler.
	if errors.Is(err, ErrSlotTaken) {
		t.Error("ErrSlotTaken vazou do allocator — o handler não deve conhecer esse erro")
	}
}

func contem(s, sub string) bool { return strings.Contains(s, sub) }
