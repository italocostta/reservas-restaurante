package reservation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"reservas-restaurante/internal/table"
)

// As interfaces são declaradas AQUI, pelo consumidor. O *PostgresRepo do
// repository.go as satisfaz sem declarar nada. Este arquivo não importa pgx,
// não importa pgconn e não importa net/http — é o que permite testar a
// validação e o roteamento de erro sem banco (Fase 1b) e a concorrência real
// com banco (Fase 1c) sem duplicar lógica.
type TableFinder interface {
	// FindCandidates devolve as mesas ativas com capacidade suficiente e SEM
	// sobreposição no intervalo, já ordenadas da menor para a maior.
	FindCandidates(ctx context.Context, partySize int, starts, ends time.Time) ([]table.Table, error)

	// GetTable busca a mesa que o staff pediu explicitamente.
	GetTable(ctx context.Context, id uuid.UUID) (table.Table, error)
}

type ReservationCreator interface {
	Insert(ctx context.Context, r Reservation) (Reservation, error)
}

// Clock existe só para que "starts_at não pode ser no passado" seja testável.
// Com time.Now() chamado direto, o teste precisaria de datas fixas no futuro —
// que expiram sozinhas e quebram o build meses depois, sem ninguém ter mexido
// em nada.
type Clock interface {
	Now() time.Time
}

// SystemClock é o relógio de verdade. Exportado porque o main.go precisa
// passá-lo explicitamente: um default implícito ("se for nil, usa o relógio do
// sistema") esconderia num if a dependência mais traiçoeira do domínio.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// ServiceHours é declarado aqui, e não importado de config: o domínio diz o que
// precisa, e cabe ao main.go traduzir a Config para isto. Se `reservation`
// importasse `config`, a regra de negócio passaria a depender de como as
// variáveis de ambiente estão organizadas.
type ServiceHours struct {
	Start time.Duration // desde a meia-noite, no fuso TZ
	End   time.Duration
	TZ    *time.Location
}

type Allocator struct {
	finder  TableFinder
	creator ReservationCreator
	hours   ServiceHours
	clock   Clock
}

func NewAllocator(finder TableFinder, creator ReservationCreator, hours ServiceHours, clock Clock) *Allocator {
	return &Allocator{finder: finder, creator: creator, hours: hours, clock: clock}
}

// maxRetries é arbitrário — débito técnico #3 da spec. Sob contenção muito alta
// dá para esgotar as tentativas mesmo havendo mesa livre, porque a vaga é tomada
// a cada nova consulta. Aceitável para o volume de um restaurante: não são
// dezenas de reservas simultâneas para o mesmo minuto.
const maxRetries = 3

// CreateReservation é a única porta de entrada do domínio. Decide entre os dois
// caminhos, e cada um trata a colisão de um jeito diferente — que é a correção
// que a primeira rodada de revisão da spec trouxe.
func (a *Allocator) CreateReservation(ctx context.Context, req AllocationRequest) (Reservation, error) {
	if err := a.validarPedido(req); err != nil {
		return Reservation{}, err
	}

	// Manual: SEM retry. A colisão é definitiva — a Mesa 12 vai colidir de novo
	// na segunda tentativa e na terceira. Não existe "próxima opção" a encontrar,
	// então reconsultar é trabalho puro sem chance de resultado diferente.
	if req.PreferredTableID != nil {
		res, err := a.tryAllocateSpecific(ctx, req)
		if errors.Is(err, ErrSlotTaken) {
			return Reservation{}, ErrTableUnavailable
		}
		return res, err
	}

	// Automático: retry faz sentido porque a reconsulta enxerga um mundo novo —
	// a mesa que acabou de ser tomada some das candidatas, e a próxima primeira
	// da fila é OUTRA mesa.
	// `for range maxRetries` — Go 1.22+. O contador não é usado no corpo, então
	// declará-lo só para incrementá-lo seria ruído.
	for range maxRetries {
		res, err := a.tryAllocateAutomatic(ctx, req)
		if err == nil {
			return res, nil
		}
		if errors.Is(err, ErrSlotTaken) {
			continue
		}
		// Qualquer outro erro é definitivo — inclusive ErrNoAvailability, que
		// significa "não existe mesa que sirva". Reconsultar não faria aparecer.
		return Reservation{}, err
	}

	// Tentativas esgotadas. Devolvemos ErrNoAvailability por ser a resposta menos
	// errada, mas ela mente um pouco: pode haver mesa livre e ter faltado
	// tentativa. É o livelock aceito conscientemente na spec — e nem o chamador
	// nem o usuário conseguem distinguir os dois casos.
	return Reservation{}, ErrNoAvailability
}

// validarPedido roda só as validações que NÃO precisam de I/O: as que dependem
// do pedido, do relógio e do horário de funcionamento. As que dependem da mesa
// (existe? está ativa? comporta o grupo?) ficam no caminho manual, porque exigem
// ir ao banco buscá-la — separar isso é o que faz esta função ser testável em
// microssegundos e sem fake nenhum.
func (a *Allocator) validarPedido(req AllocationRequest) error {
	if req.PartySize <= 0 {
		return invalido("O número de pessoas deve ser maior que zero.")
	}
	if strings.TrimSpace(req.CustomerName) == "" {
		return invalido("O nome do cliente é obrigatório.")
	}
	if strings.TrimSpace(req.CustomerPhone) == "" {
		return invalido("O telefone do cliente é obrigatório.")
	}
	if !req.StartsAt.Before(req.EndsAt) {
		return invalido("O horário de término deve ser posterior ao de início.")
	}
	if req.StartsAt.Before(a.clock.Now()) {
		return invalido("Não é possível reservar para um horário no passado.")
	}

	// Hora de parede NO FUSO DO RESTAURANTE — não em UTC, não no fuso do
	// servidor. Mesma armadilha do filtro ?date=: o instante 01:00 UTC é 22:00
	// em São Paulo, e é o 22:00 que precisa caber no expediente.
	local := req.StartsAt.In(a.hours.TZ)
	desdeMeiaNoite := time.Duration(local.Hour())*time.Hour +
		time.Duration(local.Minute())*time.Minute

	// Só o INÍCIO é checado. O término pode ultrapassar o fechamento — é a
	// última mesa do dia, que senta às 22h30 e sai à meia-noite. Decisão
	// registrada na validação #8 da spec.
	if desdeMeiaNoite < a.hours.Start || desdeMeiaNoite >= a.hours.End {
		return invalido(
			"O restaurante atende das %s às %s. O horário solicitado (%s) está fora do expediente.",
			formatarHora(a.hours.Start), formatarHora(a.hours.End), local.Format("15:04"),
		)
	}

	return nil
}

func formatarHora(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(d.Hours()), int(d.Minutes())%60)
}

// tryAllocateSpecific é o caminho manual: o staff escolheu a mesa. Aqui entram
// as validações que dependem dela (spec, #4 e #5) — as únicas que precisam de
// I/O antes do INSERT.
//
// NÃO existe checagem prévia de sobreposição, e não é esquecimento: um SELECT
// antes do INSERT só somaria um round-trip e uma janela de corrida. A colisão
// já volta do banco como ErrSlotTaken, que o CreateReservation converte em
// ErrTableUnavailable — a mensagem amigável existe sem o SELECT.
//
// O ErrSlotTaken sobe daqui INTACTO. Quem decide se ele vira ErrTableUnavailable
// (manual) ou gatilho de retry (automático) é o CreateReservation. Esta função
// não sabe em qual dos dois caminhos está sendo usada.
func (a *Allocator) tryAllocateSpecific(ctx context.Context, req AllocationRequest) (Reservation, error) {
	mesa, err := a.finder.GetTable(ctx, *req.PreferredTableID)
	if errors.Is(err, table.ErrNotFound) {
		return Reservation{}, invalido("A mesa informada não existe.")
	}
	if err != nil {
		return Reservation{}, err
	}

	if !mesa.IsActive {
		return Reservation{}, invalido("A mesa %q está inativa e não aceita reservas.", mesa.Name)
	}
	if req.PartySize > mesa.Capacity {
		return Reservation{}, invalido(
			"Grupo de %d pessoas excede a capacidade da mesa %q (%d).",
			req.PartySize, mesa.Name, mesa.Capacity,
		)
	}

	return a.creator.Insert(ctx, novaReserva(req, mesa.ID))
}

// tryAllocateAutomatic é o caminho da heurística gulosa. Repare no tamanho: o
// SQL já devolve as candidatas filtradas por capacidade, sem sobreposição, e
// ordenadas da menor mesa suficiente para a maior. O "guloso" é `candidatas[0]`.
// Não há decisão de seleção escondida em Go — e é exatamente por isso que a
// spec teve que reescrever o objetivo da Fase 1b.
//
// Lista vazia devolve ErrNoAvailability na hora, e NÃO ErrSlotTaken. A distinção
// é o que impede o retry de girar em falso: "não existe mesa que sirva" é
// definitivo, e reconsultar três vezes não vai fazer aparecer uma.
func (a *Allocator) tryAllocateAutomatic(ctx context.Context, req AllocationRequest) (Reservation, error) {
	candidatas, err := a.finder.FindCandidates(ctx, req.PartySize, req.StartsAt, req.EndsAt)
	if err != nil {
		return Reservation{}, err
	}
	if len(candidatas) == 0 {
		return Reservation{}, ErrNoAvailability
	}

	// Só a primeira. Se ela colidir, o ErrSlotTaken sobe e o CreateReservation
	// RECONSULTA — não itera candidatas[1] daqui. A lista é um retrato de um
	// instante que já passou: se alguém tomou a candidatas[0], pode ter tomado
	// a [1] também. Reconsultar custa um round-trip e devolve a verdade.
	return a.creator.Insert(ctx, novaReserva(req, candidatas[0].ID))
}

// novaReserva monta a linha a inserir. Status fica zerado de propósito: o
// DEFAULT 'confirmed' do schema é a fonte da verdade, e o RETURNING traz de volta.
func novaReserva(req AllocationRequest, tableID uuid.UUID) Reservation {
	return Reservation{
		TableID:       tableID,
		CustomerName:  strings.TrimSpace(req.CustomerName),
		CustomerPhone: strings.TrimSpace(req.CustomerPhone),
		PartySize:     req.PartySize,
		StartsAt:      req.StartsAt,
		EndsAt:        req.EndsAt,
	}
}
