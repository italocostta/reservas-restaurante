package reservation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"reservas-restaurante/internal/table"
)

// Window é um intervalo [StartsAt, EndsAt). Mesmos limites da tstzrange no
// banco: o fim de uma janela é o começo da próxima, sem sobreposição.
type Window struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

// ScheduleReader é o que a agenda consome. Note que ela pede GetTable de novo —
// e isso não é duplicação com a TableFinder: são consumidores diferentes, cada
// um declarando o mínimo de que precisa. O *PostgresRepo satisfaz as duas.
//
// Os dois métodos de baixo são o par dos dois de cima, no plural: a mesma
// pergunta feita para o salão inteiro em vez de para uma mesa. Eles estão na
// MESMA interface, e não numa DayGridReader separada, porque o consumidor é o
// mesmo tipo — o *Schedule. Interface segregada por consumidor, não por método.
type ScheduleReader interface {
	GetTable(ctx context.Context, id uuid.UUID) (table.Table, error)
	BusyWindows(ctx context.Context, tableID uuid.UUID, from, to time.Time) ([]Window, error)

	ListActiveTables(ctx context.Context) ([]table.Table, error)
	BusyWindowsAll(ctx context.Context, from, to time.Time) (map[uuid.UUID][]Window, error)
}

// TableAvailability é uma linha da grade do dia: a mesa, e quando ela está livre.
//
// Carrega name e capacity, e não só o table_id, porque a tela que a consome
// precisa dos três juntos — o staff escolhe a mesa para combinar olhando "Mesa
// 04, 4 lugares, livre das 20h30". Devolver só os ids obrigaria o frontend a
// cruzar esta resposta com um GET /tables, e um JOIN feito no cliente é um JOIN
// feito no lugar errado.
type TableAvailability struct {
	TableID     uuid.UUID `json:"table_id"`
	TableName   string    `json:"table_name" example:"Mesa 04"`
	Capacity    int       `json:"capacity"   example:"4"`
	FreeWindows []Window  `json:"free_windows"`
}

type Schedule struct {
	repo  ScheduleReader
	hours ServiceHours
}

func NewSchedule(repo ScheduleReader, hours ServiceHours) *Schedule {
	return &Schedule{repo: repo, hours: hours}
}

func (s *Schedule) FreeWindows(ctx context.Context, tableID uuid.UUID, dia string) ([]Window, error) {
	expediente, err := s.expedienteDe(dia)
	if err != nil {
		return nil, err
	}

	mesa, err := s.repo.GetTable(ctx, tableID)
	if errors.Is(err, table.ErrNotFound) {
		return nil, invalido("A mesa informada não existe.")
	}
	if err != nil {
		return nil, err
	}
	// Mesa inativa não tem janela livre — e isso é diferente de "não existe".
	// Lista vazia é a resposta honesta: a mesa existe, e nenhuma hora dela é
	// reservável.
	if !mesa.IsActive {
		return []Window{}, nil
	}

	ocupadas, err := s.repo.BusyWindows(ctx, tableID, expediente.StartsAt, expediente.EndsAt)
	if err != nil {
		return nil, err
	}

	return janelasLivres(expediente, ocupadas), nil
}

// DayGrid é o FreeWindows para o salão inteiro: cada mesa ativa e suas janelas
// livres no dia. É o que a tela de combinação da Fase 3a precisa para o staff
// poder escolher QUAIS mesas juntar — pergunta que, até aqui, a API não sabia
// responder sem N requisições.
//
// Repare no que esta função NÃO faz: ela não recalcula janela nenhuma. O sweep
// continua sendo o janelasLivres, exatamente o mesmo, chamado uma vez por mesa
// sobre dados que já vieram do banco numa query só. Duas idas ao banco no total,
// independentemente do tamanho do salão.
//
// E é justamente por ele existir que o frontend NÃO precisa reimplementar o sweep
// em JavaScript: "quais mesas estão livres das 20h às 22h?" vira uma checagem de
// contenção sobre estas janelas — três linhas de filtro, não um algoritmo. A
// única lógica de domínio de verdade do projeto continua morando num lugar só.
func (s *Schedule) DayGrid(ctx context.Context, dia string) ([]TableAvailability, error) {
	expediente, err := s.expedienteDe(dia)
	if err != nil {
		return nil, err
	}

	mesas, err := s.repo.ListActiveTables(ctx)
	if err != nil {
		return nil, err
	}

	ocupadasPorMesa, err := s.repo.BusyWindowsAll(ctx, expediente.StartsAt, expediente.EndsAt)
	if err != nil {
		return nil, err
	}

	grade := make([]TableAvailability, 0, len(mesas))
	for _, m := range mesas {
		// ocupadasPorMesa[m.ID] numa mesa sem reserva devolve nil — e
		// janelasLivres(expediente, nil) devolve o expediente inteiro livre, que é
		// a resposta certa. O map faz o caso da mesa vazia desaparecer.
		grade = append(grade, TableAvailability{
			TableID:     m.ID,
			TableName:   m.Name,
			Capacity:    m.Capacity,
			FreeWindows: janelasLivres(expediente, ocupadasPorMesa[m.ID]),
		})
	}

	return grade, nil
}

// expedienteDe transforma "2026-07-20" na janela de atendimento daquele dia,
// nos instantes certos.
//
// ParseInLocation, e não Parse: o Parse interpretaria a data em UTC, e a janela
// sairia deslocada em três horas. É a mesma armadilha do ?date= e da validação
// de expediente — a terceira vez que ela aparece, sempre disfarçada.
func (s *Schedule) expedienteDe(dia string) (Window, error) {
	d, err := time.ParseInLocation(time.DateOnly, dia, s.hours.TZ)
	if err != nil {
		return Window{}, invalido("Parâmetro 'date' deve estar no formato AAAA-MM-DD.")
	}

	return Window{
		StartsAt: d.Add(s.hours.Start),
		EndsAt:   d.Add(s.hours.End),
	}, nil
}

// janelasLivres subtrai as janelas ocupadas do expediente. `ocupadas` precisa
// vir ordenada por início — o ORDER BY do SQL garante isso.
//
// É a única lógica de domínio deste projeto que NÃO dava para empurrar para o
// SQL sem sofrimento, e por isso a única que merece teste de unidade de verdade.
func janelasLivres(expediente Window, ocupadas []Window) []Window {
	livres := []Window{}
	cursor := expediente.StartsAt

	for _, oc := range ocupadas {
		// Recorta a ocupada para dentro do expediente. Uma reserva das 22h30 à
		// 00h30 (permitida — é a última mesa do dia) só bloqueia até as 23h do
		// ponto de vista da janela deste dia.
		inicio := maisTarde(oc.StartsAt, expediente.StartsAt)
		fim := maisCedo(oc.EndsAt, expediente.EndsAt)
		if !inicio.Before(fim) {
			continue // não intersecta o expediente
		}

		if cursor.Before(inicio) {
			livres = append(livres, Window{StartsAt: cursor, EndsAt: inicio})
		}
		// O avanço condicional do cursor tolera ocupadas que se sobrepõem ou se
		// contêm. A constraint EXCLUDE impede que isso aconteça hoje, mas o
		// algoritmo não deve depender disso para não devolver lixo se um dia
		// alguém inserir dados na marra.
		if fim.After(cursor) {
			cursor = fim
		}
	}

	if cursor.Before(expediente.EndsAt) {
		livres = append(livres, Window{StartsAt: cursor, EndsAt: expediente.EndsAt})
	}

	return livres
}

func maisTarde(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func maisCedo(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
