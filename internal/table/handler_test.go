package table

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fakeRepo satisfaz a interface `repository` declarada em handler.go — a mesma
// que *PostgresRepo satisfaz. Sem banco, sem mock gerado, sem anotação: só uma
// struct que por acaso tem os quatro métodos certos.
type fakeRepo struct {
	createFn func(ctx context.Context, name string, capacity int) (Table, error)
	listFn   func(ctx context.Context, active *bool) ([]Table, error)
	getFn    func(ctx context.Context, id uuid.UUID) (Table, error)
	updateFn func(ctx context.Context, id uuid.UUID, p UpdateParams) (Table, error)

	calls int // quantas vezes o repositório foi tocado
}

func (f *fakeRepo) Create(ctx context.Context, name string, capacity int) (Table, error) {
	f.calls++
	return f.createFn(ctx, name, capacity)
}

func (f *fakeRepo) List(ctx context.Context, active *bool) ([]Table, error) {
	f.calls++
	return f.listFn(ctx, active)
}

func (f *fakeRepo) GetByID(ctx context.Context, id uuid.UUID) (Table, error) {
	f.calls++
	return f.getFn(ctx, id)
}

func (f *fakeRepo) Update(ctx context.Context, id uuid.UUID, p UpdateParams) (Table, error) {
	f.calls++
	return f.updateFn(ctx, id, p)
}

func ptr[T any](v T) *T { return &v }

func TestCreate(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		repoErr    error
		wantStatus int
		wantCalls  int // 0 = validação barrou antes de tocar no repositório
	}{
		{"sucesso", `{"name":"Mesa 12","capacity":4}`, nil, http.StatusCreated, 1},
		{"nome duplicado", `{"name":"Mesa 12","capacity":4}`, ErrDuplicateName, http.StatusConflict, 1},
		{"json malformado", `{`, nil, http.StatusBadRequest, 0},
		{"campo desconhecido", `{"nome":"Mesa 12","capacity":4}`, nil, http.StatusBadRequest, 0},
		{"nome vazio", `{"name":"   ","capacity":4}`, nil, http.StatusBadRequest, 0},
		{"capacidade zero", `{"name":"Mesa 12","capacity":0}`, nil, http.StatusBadRequest, 0},
		{"capacidade estoura smallint", `{"name":"Mesa 12","capacity":99999}`, nil, http.StatusBadRequest, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{
				createFn: func(_ context.Context, name string, capacity int) (Table, error) {
					if tc.repoErr != nil {
						return Table{}, tc.repoErr
					}
					return Table{ID: uuid.New(), Name: name, Capacity: capacity, IsActive: true}, nil
				},
			}

			req := httptest.NewRequest(http.MethodPost, "/tables", bytes.NewBufferString(tc.body))
			rec := httptest.NewRecorder()

			NewHandler(repo).Create(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, quero %d (corpo: %s)", rec.Code, tc.wantStatus, rec.Body)
			}
			if repo.calls != tc.wantCalls {
				t.Errorf("repositório chamado %d vez(es), quero %d", repo.calls, tc.wantCalls)
			}
		})
	}
}

func TestCreateAparaEspacosDoNome(t *testing.T) {
	var recebido string
	repo := &fakeRepo{
		createFn: func(_ context.Context, name string, capacity int) (Table, error) {
			recebido = name
			return Table{Name: name, Capacity: capacity}, nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/tables", strings.NewReader(`{"name":"  Mesa 12  ","capacity":4}`))
	NewHandler(repo).Create(httptest.NewRecorder(), req)

	if recebido != "Mesa 12" {
		t.Errorf("repositório recebeu %q, quero %q", recebido, "Mesa 12")
	}
}

// Lista vazia tem que sair como [] e nunca como null: um slice nil serializa
// como null em JSON e quebra o .map() do frontend.
func TestListVaziaSerializaComoArray(t *testing.T) {
	repo := &fakeRepo{
		listFn: func(context.Context, *bool) ([]Table, error) { return []Table{}, nil },
	}

	rec := httptest.NewRecorder()
	NewHandler(repo).List(rec, httptest.NewRequest(http.MethodGet, "/tables", nil))

	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("corpo = %s, quero []", got)
	}
}

// O tri-state precisa chegar intacto no repositório: ausente é nil, e
// active=false é ponteiro para false — não nil.
func TestListFiltroActive(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		wantActive *bool
		wantStatus int
		wantCalls  int
	}{
		{"sem filtro", "/tables", nil, http.StatusOK, 1},
		{"active=true", "/tables?active=true", ptr(true), http.StatusOK, 1},
		{"active=false", "/tables?active=false", ptr(false), http.StatusOK, 1},
		{"valor inválido", "/tables?active=talvez", nil, http.StatusBadRequest, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var recebido *bool
			repo := &fakeRepo{
				listFn: func(_ context.Context, active *bool) ([]Table, error) {
					recebido = active
					return []Table{}, nil
				},
			}

			rec := httptest.NewRecorder()
			NewHandler(repo).List(rec, httptest.NewRequest(http.MethodGet, tc.url, nil))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, quero %d", rec.Code, tc.wantStatus)
			}
			if repo.calls != tc.wantCalls {
				t.Fatalf("repositório chamado %d vez(es), quero %d", repo.calls, tc.wantCalls)
			}
			if tc.wantCalls == 0 {
				return
			}

			switch {
			case tc.wantActive == nil && recebido != nil:
				t.Errorf("repositório recebeu &%v, quero nil", *recebido)
			case tc.wantActive != nil && recebido == nil:
				t.Errorf("repositório recebeu nil, quero &%v", *tc.wantActive)
			case tc.wantActive != nil && *recebido != *tc.wantActive:
				t.Errorf("repositório recebeu &%v, quero &%v", *recebido, *tc.wantActive)
			}
		})
	}
}

func TestGet(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		repoErr    error
		wantStatus int
		wantCalls  int
	}{
		{"sucesso", uuid.NewString(), nil, http.StatusOK, 1},
		{"não encontrada", uuid.NewString(), ErrNotFound, http.StatusNotFound, 1},
		{"id não é uuid", "abc", nil, http.StatusBadRequest, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{
				getFn: func(_ context.Context, id uuid.UUID) (Table, error) {
					if tc.repoErr != nil {
						return Table{}, tc.repoErr
					}
					return Table{ID: id, Name: "Mesa 12", Capacity: 4}, nil
				},
			}

			req := httptest.NewRequest(http.MethodGet, "/tables/"+tc.id, nil)
			req.SetPathValue("id", tc.id)
			rec := httptest.NewRecorder()

			NewHandler(repo).Get(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, quero %d (corpo: %s)", rec.Code, tc.wantStatus, rec.Body)
			}
			if repo.calls != tc.wantCalls {
				t.Errorf("repositório chamado %d vez(es), quero %d", repo.calls, tc.wantCalls)
			}
		})
	}
}

// A razão de existir dos ponteiros em updateRequest: {"is_active": false} tem
// que chegar no repositório como ponteiro para false, e não como nil — que é o
// que um bool comum produziria, tornando "desativar" indistinguível de "não mexer".
func TestUpdateDesativarMesa(t *testing.T) {
	id := uuid.New()
	var recebido UpdateParams

	repo := &fakeRepo{
		updateFn: func(_ context.Context, _ uuid.UUID, p UpdateParams) (Table, error) {
			recebido = p
			return Table{ID: id, Name: "Mesa 12", Capacity: 4, IsActive: false}, nil
		},
	}

	req := httptest.NewRequest(http.MethodPatch, "/tables/"+id.String(), strings.NewReader(`{"is_active":false}`))
	req.SetPathValue("id", id.String())
	rec := httptest.NewRecorder()

	NewHandler(repo).Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo: %s)", rec.Code, rec.Body)
	}
	if recebido.IsActive == nil {
		t.Fatal("repositório recebeu IsActive nil — o false foi perdido")
	}
	if *recebido.IsActive != false {
		t.Errorf("repositório recebeu IsActive = %v, quero false", *recebido.IsActive)
	}
	if recebido.Name != nil || recebido.Capacity != nil {
		t.Error("campos não enviados no PATCH deveriam chegar como nil")
	}
}

func TestUpdate(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		repoErr    error
		wantStatus int
		wantCalls  int
	}{
		{"sucesso", `{"capacity":6}`, nil, http.StatusOK, 1},
		{"não encontrada", `{"capacity":6}`, ErrNotFound, http.StatusNotFound, 1},
		{"nome duplicado", `{"name":"Mesa 12"}`, ErrDuplicateName, http.StatusConflict, 1},
		{"corpo vazio", `{}`, nil, http.StatusBadRequest, 0},
		{"nome em branco", `{"name":"  "}`, nil, http.StatusBadRequest, 0},
		{"capacidade zero", `{"capacity":0}`, nil, http.StatusBadRequest, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			repo := &fakeRepo{
				updateFn: func(_ context.Context, _ uuid.UUID, _ UpdateParams) (Table, error) {
					if tc.repoErr != nil {
						return Table{}, tc.repoErr
					}
					return Table{ID: id}, nil
				},
			}

			req := httptest.NewRequest(http.MethodPatch, "/tables/"+id.String(), strings.NewReader(tc.body))
			req.SetPathValue("id", id.String())
			rec := httptest.NewRecorder()

			NewHandler(repo).Update(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, quero %d (corpo: %s)", rec.Code, tc.wantStatus, rec.Body)
			}
			if repo.calls != tc.wantCalls {
				t.Errorf("repositório chamado %d vez(es), quero %d", repo.calls, tc.wantCalls)
			}
		})
	}
}
