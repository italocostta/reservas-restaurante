package reservation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ---------- fakes das TRÊS interfaces que o handler consome ----------
//
// Nenhuma delas é a mesma que o allocator consome (TableFinder/ReservationCreator):
// cada consumidor declara a sua, e o *PostgresRepo real satisfaz todas sem saber
// que existem. Os contadores existem para provar o que os status codes não provam:
// que entrada inválida NÃO chega no domínio nem no banco.

type fakeAllocator struct {
	fn    func(ctx context.Context, req AllocationRequest) (Reservation, error)
	calls int
	visto AllocationRequest // o último pedido que chegou, já convertido
}

func (f *fakeAllocator) CreateReservation(ctx context.Context, req AllocationRequest) (Reservation, error) {
	f.calls++
	f.visto = req
	return f.fn(ctx, req)
}

type fakeRepo struct {
	getFn    func(context.Context, uuid.UUID) (Reservation, error)
	listFn   func(context.Context, ListFilter) ([]Reservation, error)
	cancelFn func(context.Context, uuid.UUID) error

	calls  int
	filtro ListFilter // o último filtro que chegou, já parseado
}

func (f *fakeRepo) Get(ctx context.Context, id uuid.UUID) (Reservation, error) {
	f.calls++
	return f.getFn(ctx, id)
}

func (f *fakeRepo) List(ctx context.Context, filtro ListFilter) ([]Reservation, error) {
	f.calls++
	f.filtro = filtro
	return f.listFn(ctx, filtro)
}

func (f *fakeRepo) Cancel(ctx context.Context, id uuid.UUID) error {
	f.calls++
	return f.cancelFn(ctx, id)
}

type fakeSchedule struct {
	fn    func(context.Context, uuid.UUID, string) ([]Window, error)
	calls int
	dia   string // o último dia que chegou
}

func (f *fakeSchedule) FreeWindows(ctx context.Context, id uuid.UUID, dia string) ([]Window, error) {
	f.calls++
	f.dia = dia
	return f.fn(ctx, id, dia)
}

func comPathValue(r *http.Request, id uuid.UUID) *http.Request {
	r.SetPathValue("id", id.String())
	return r
}

// corpo válido de referência — os testes o alteram pontualmente
const corpoValido = `{"customer_name":"Maria Silva","customer_phone":"11999998888",` +
	`"party_size":4,"starts_at":"2026-07-20T19:00:00-03:00","ends_at":"2026-07-20T21:00:00-03:00"}`

func mensagemDeErro(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var corpo struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &corpo); err != nil {
		t.Fatalf("corpo não é o formato de erro esperado: %s", rec.Body)
	}
	return corpo.Error
}

// ---------- POST /reservations ----------

func TestCreateTraduzErroDeDominioEmStatus(t *testing.T) {
	res := Reservation{ID: uuid.New(), Status: StatusConfirmed}

	casos := []struct {
		nome       string
		corpo      string
		erroDoDom  error
		wantStatus int
		wantCalls  int // 0 = o handler barrou antes de chamar o domínio
	}{
		{"sucesso", corpoValido, nil, http.StatusCreated, 1},
		{"json malformado", `{`, nil, http.StatusBadRequest, 0},
		{"campo desconhecido", `{"nome":"Maria"}`, nil, http.StatusBadRequest, 0},
		{"validação de domínio", corpoValido,
			invalido("Grupo de 6 pessoas excede a capacidade da mesa (4)."), http.StatusBadRequest, 1},
		{"sem disponibilidade", corpoValido, ErrNoAvailability, http.StatusConflict, 1},
		{"mesa pedida ocupada", corpoValido, ErrTableUnavailable, http.StatusConflict, 1},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			alloc := &fakeAllocator{fn: func(context.Context, AllocationRequest) (Reservation, error) {
				if tc.erroDoDom != nil {
					return Reservation{}, tc.erroDoDom
				}
				return res, nil
			}}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/reservations", strings.NewReader(tc.corpo))

			NewHandler(alloc, &fakeRepo{}, &fakeSchedule{}).Create(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, quero %d (corpo: %s)", rec.Code, tc.wantStatus, rec.Body)
			}
			if alloc.calls != tc.wantCalls {
				t.Errorf("domínio chamado %d vez(es), quero %d", alloc.calls, tc.wantCalls)
			}
		})
	}
}

// A mensagem do ValidationError tem que sair INTEIRA e LIMPA para o usuário —
// é a razão de ele ser um tipo e não mais uma sentinela.
func TestCreatePassaAMensagemDeValidacaoIntacta(t *testing.T) {
	const msg = "Grupo de 6 pessoas excede a capacidade da mesa (4)."

	alloc := &fakeAllocator{fn: func(context.Context, AllocationRequest) (Reservation, error) {
		return Reservation{}, invalido("%s", msg)
	}}

	rec := httptest.NewRecorder()
	NewHandler(alloc, &fakeRepo{}, &fakeSchedule{}).Create(
		rec, httptest.NewRequest(http.MethodPost, "/reservations", strings.NewReader(corpoValido)))

	if got := mensagemDeErro(t, rec); got != msg {
		t.Errorf("mensagem = %q, quero %q — o handler não pode reescrever nem prefixar", got, msg)
	}
}

// O INVARIANTE. ErrSlotTaken é sinal interno entre repositório e allocator; se
// chegar ao handler, é bug de programação, não erro de usuário. Tem que virar
// 500 — nunca um 409 disfarçado, que esconderia o defeito para sempre. E a
// mensagem interna ("horário já ocupado nessa mesa") não pode vazar no corpo.
func TestCreateNaoDisfarcaErrSlotTakenDe409(t *testing.T) {
	alloc := &fakeAllocator{fn: func(context.Context, AllocationRequest) (Reservation, error) {
		return Reservation{}, ErrSlotTaken
	}}

	rec := httptest.NewRecorder()
	NewHandler(alloc, &fakeRepo{}, &fakeSchedule{}).Create(
		rec, httptest.NewRequest(http.MethodPost, "/reservations", strings.NewReader(corpoValido)))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, quero 500 — ErrSlotTaken vazando é bug meu, não erro do usuário", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "ocupado") {
		t.Errorf("a mensagem interna vazou no corpo: %s", rec.Body)
	}
}

// A conversão DTO → domínio: seis linhas no handler que em Java seriam MapStruct.
func TestCreateConverteORequestParaODominio(t *testing.T) {
	mesaID := uuid.New()

	casos := []struct {
		nome        string
		tableIDJSON string
		querMesa    *uuid.UUID
	}{
		{"sem table_id: aloca automaticamente", ``, nil},
		{"table_id null: aloca automaticamente", `"table_id":null,`, nil},
		{"table_id informado: usa a mesa pedida", `"table_id":"` + mesaID.String() + `",`, &mesaID},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			alloc := &fakeAllocator{fn: func(_ context.Context, r AllocationRequest) (Reservation, error) {
				return Reservation{}, nil
			}}

			corpo := `{` + tc.tableIDJSON + `"customer_name":"Maria Silva","customer_phone":"11999998888",` +
				`"party_size":4,"starts_at":"2026-07-20T19:00:00-03:00","ends_at":"2026-07-20T21:00:00-03:00"}`

			NewHandler(alloc, &fakeRepo{}, &fakeSchedule{}).Create(
				httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/reservations", strings.NewReader(corpo)))

			if alloc.calls != 1 {
				t.Fatalf("domínio chamado %d vez(es), quero 1", alloc.calls)
			}

			switch {
			case tc.querMesa == nil && alloc.visto.PreferredTableID != nil:
				t.Errorf("PreferredTableID = %v, quero nil (heurística automática)", *alloc.visto.PreferredTableID)
			case tc.querMesa != nil && alloc.visto.PreferredTableID == nil:
				t.Error("PreferredTableID = nil, quero a mesa pedida — o caminho manual foi perdido")
			case tc.querMesa != nil && *alloc.visto.PreferredTableID != *tc.querMesa:
				t.Errorf("PreferredTableID = %v, quero %v", *alloc.visto.PreferredTableID, *tc.querMesa)
			}

			// O instante tem que sobreviver ao parse do JSON.
			if !alloc.visto.StartsAt.Equal(time.Date(2026, 7, 20, 19, 0, 0, 0, fusoSP)) {
				t.Errorf("StartsAt = %v, quero 20/07/2026 19:00 -03:00", alloc.visto.StartsAt)
			}
		})
	}
}

// ---------- GET /reservations ----------

func TestListParseiaOsFiltros(t *testing.T) {
	mesaID := uuid.New()

	casos := []struct {
		nome       string
		query      string
		wantStatus int
		wantCalls  int
		confere    func(*testing.T, ListFilter)
	}{
		{
			nome: "sem filtros: tudo nil", query: "", wantStatus: http.StatusOK, wantCalls: 1,
			confere: func(t *testing.T, f ListFilter) {
				if f.Date != nil || f.TableID != nil || f.Status != nil {
					t.Errorf("filtro = %+v, quero tudo nil", f)
				}
			},
		},
		{
			nome: "date válida", query: "?date=2026-07-20", wantStatus: http.StatusOK, wantCalls: 1,
			confere: func(t *testing.T, f ListFilter) {
				// String, e não time.Time: converter em Go escolheria um fuso, e
				// escolheria errado. Quem transforma "o dia" em instantes é o
				// Postgres, com o AT TIME ZONE do fuso do restaurante.
				if f.Date == nil || *f.Date != "2026-07-20" {
					t.Errorf("Date = %v, quero \"2026-07-20\"", f.Date)
				}
			},
		},
		{
			nome: "status válido", query: "?status=cancelled", wantStatus: http.StatusOK, wantCalls: 1,
			confere: func(t *testing.T, f ListFilter) {
				if f.Status == nil || *f.Status != StatusCancelled {
					t.Errorf("Status = %v, quero cancelled", f.Status)
				}
			},
		},
		{
			nome: "table_id válido", query: "?table_id=" + mesaID.String(), wantStatus: http.StatusOK, wantCalls: 1,
			confere: func(t *testing.T, f ListFilter) {
				if f.TableID == nil || *f.TableID != mesaID {
					t.Errorf("TableID = %v, quero %v", f.TableID, mesaID)
				}
			},
		},
		// O tipo nomeado Status é uma cerca, não um muro: Status("talvez") compila.
		// É AQUI, na fronteira, que a checagem de verdade tem que estar.
		{nome: "status fora do enum", query: "?status=talvez", wantStatus: http.StatusBadRequest, wantCalls: 0},
		{nome: "date em formato brasileiro", query: "?date=20/07/2026", wantStatus: http.StatusBadRequest, wantCalls: 0},
		{nome: "table_id não é uuid", query: "?table_id=abc", wantStatus: http.StatusBadRequest, wantCalls: 0},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			repo := &fakeRepo{listFn: func(context.Context, ListFilter) ([]Reservation, error) {
				return []Reservation{}, nil
			}}

			rec := httptest.NewRecorder()
			NewHandler(&fakeAllocator{}, repo, &fakeSchedule{}).List(
				rec, httptest.NewRequest(http.MethodGet, "/reservations"+tc.query, nil))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, quero %d (corpo: %s)", rec.Code, tc.wantStatus, rec.Body)
			}
			if repo.calls != tc.wantCalls {
				t.Fatalf("repositório chamado %d vez(es), quero %d", repo.calls, tc.wantCalls)
			}
			if tc.confere != nil {
				tc.confere(t, repo.filtro)
			}
		})
	}
}

// Lista vazia sai como [] e nunca como null — slice nil serializa como null e
// quebra o .map() do frontend.
func TestListVaziaSerializaComoArray(t *testing.T) {
	repo := &fakeRepo{listFn: func(context.Context, ListFilter) ([]Reservation, error) {
		return []Reservation{}, nil
	}}

	rec := httptest.NewRecorder()
	NewHandler(&fakeAllocator{}, repo, &fakeSchedule{}).List(
		rec, httptest.NewRequest(http.MethodGet, "/reservations", nil))

	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("corpo = %s, quero []", got)
	}
}

// ---------- GET e DELETE /reservations/{id} ----------

func TestGetEDelete(t *testing.T) {
	id := uuid.New()

	casos := []struct {
		nome       string
		id         string
		erroDoRepo error
		wantStatus map[string]int // por método
		wantCalls  int
	}{
		{"sucesso", id.String(), nil,
			map[string]int{"GET": http.StatusOK, "DELETE": http.StatusNoContent}, 1},
		{"não encontrada", id.String(), ErrNotFound,
			map[string]int{"GET": http.StatusNotFound, "DELETE": http.StatusNotFound}, 1},
		{"id não é uuid", "abc", nil,
			map[string]int{"GET": http.StatusBadRequest, "DELETE": http.StatusBadRequest}, 0},
	}

	for _, tc := range casos {
		t.Run("GET/"+tc.nome, func(t *testing.T) {
			repo := &fakeRepo{getFn: func(_ context.Context, id uuid.UUID) (Reservation, error) {
				if tc.erroDoRepo != nil {
					return Reservation{}, tc.erroDoRepo
				}
				return Reservation{ID: id, Status: StatusConfirmed}, nil
			}}

			req := httptest.NewRequest(http.MethodGet, "/reservations/"+tc.id, nil)
			req.SetPathValue("id", tc.id)
			rec := httptest.NewRecorder()

			NewHandler(&fakeAllocator{}, repo, &fakeSchedule{}).Get(rec, req)

			if rec.Code != tc.wantStatus["GET"] {
				t.Errorf("status = %d, quero %d", rec.Code, tc.wantStatus["GET"])
			}
			if repo.calls != tc.wantCalls {
				t.Errorf("repositório chamado %d vez(es), quero %d", repo.calls, tc.wantCalls)
			}
		})

		t.Run("DELETE/"+tc.nome, func(t *testing.T) {
			repo := &fakeRepo{cancelFn: func(context.Context, uuid.UUID) error { return tc.erroDoRepo }}

			req := httptest.NewRequest(http.MethodDelete, "/reservations/"+tc.id, nil)
			req.SetPathValue("id", tc.id)
			rec := httptest.NewRecorder()

			NewHandler(&fakeAllocator{}, repo, &fakeSchedule{}).Delete(rec, req)

			if rec.Code != tc.wantStatus["DELETE"] {
				t.Errorf("status = %d, quero %d", rec.Code, tc.wantStatus["DELETE"])
			}
			if repo.calls != tc.wantCalls {
				t.Errorf("repositório chamado %d vez(es), quero %d", repo.calls, tc.wantCalls)
			}
			// 204 não pode ter corpo.
			if rec.Code == http.StatusNoContent && rec.Body.Len() != 0 {
				t.Errorf("204 veio com corpo: %s", rec.Body)
			}
		})
	}
}

// ---------- GET /tables/{id}/availability ----------

func TestAvailability(t *testing.T) {
	mesaID := uuid.New()

	casos := []struct {
		nome       string
		id         string
		query      string
		wantStatus int
		wantCalls  int
	}{
		{"sucesso", mesaID.String(), "?date=2026-07-20", http.StatusOK, 1},
		// date é OBRIGATÓRIO aqui, ao contrário dos filtros do List: "janelas
		// livres" sem dia é pergunta sem resposta, não pergunta sem filtro.
		{"sem date", mesaID.String(), "", http.StatusBadRequest, 0},
		{"id não é uuid", "abc", "?date=2026-07-20", http.StatusBadRequest, 0},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			sched := &fakeSchedule{fn: func(context.Context, uuid.UUID, string) ([]Window, error) {
				return []Window{jan(20, 18, 0, 20, 19, 0)}, nil
			}}

			req := httptest.NewRequest(http.MethodGet, "/tables/"+tc.id+"/availability"+tc.query, nil)
			req.SetPathValue("id", tc.id)
			rec := httptest.NewRecorder()

			NewHandler(&fakeAllocator{}, &fakeRepo{}, sched).Availability(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, quero %d (corpo: %s)", rec.Code, tc.wantStatus, rec.Body)
			}
			if sched.calls != tc.wantCalls {
				t.Errorf("agenda chamada %d vez(es), quero %d", sched.calls, tc.wantCalls)
			}
			if tc.wantCalls == 1 && sched.dia != "2026-07-20" {
				t.Errorf("dia repassado = %q, quero \"2026-07-20\"", sched.dia)
			}
		})
	}
}
