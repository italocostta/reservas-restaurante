package table

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"reservas-restaurante/internal/httpx"
)

// repository é declarada aqui, pelo consumidor, e não em repository.go pelo
// provedor. *PostgresRepo a satisfaz sem declarar nada — não existe `implements`.
// É isso que deixa handler_test.go rodar contra um fake em memória, sem banco.
type repository interface {
	Create(ctx context.Context, name string, capacity int) (Table, error)
	List(ctx context.Context, active *bool) ([]Table, error)
	GetByID(ctx context.Context, id uuid.UUID) (Table, error)
	Update(ctx context.Context, id uuid.UUID, p UpdateParams) (Table, error)
}

type Handler struct {
	repo repository
}

func NewHandler(repo repository) *Handler {
	return &Handler{repo: repo}
}

type createRequest struct {
	Name     string `json:"name"     example:"Mesa 12"`
	Capacity int    `json:"capacity" example:"4"`
}

// Create godoc
//
//	@Summary		Cria uma mesa
//	@Description	Cadastra uma mesa nova. O nome é único no restaurante.
//	@Tags			mesas
//	@Accept			json
//	@Produce		json
//	@Param			mesa	body		createRequest	true	"Dados da mesa"
//	@Success		201		{object}	Table
//	@Failure		400		{object}	httpx.ErrorResponse	"Corpo inválido, nome vazio ou capacidade fora do intervalo"
//	@Failure		409		{object}	httpx.ErrorResponse	"Já existe mesa com esse nome"
//	@Router			/tables [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req createRequest

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "corpo da requisição inválido.")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpx.Error(w, http.StatusBadRequest, "nome da mesa é obrigatório.")
		return
	}
	// O limite superior existe porque capacity é smallint no banco: sem ele,
	// um capacity de 99999 viraria erro 22003 do Postgres e sairia como 500 —
	// resposta errada para uma entrada inválida do usuário.
	if req.Capacity <= 0 || req.Capacity > math.MaxInt16 {
		httpx.Error(w, http.StatusBadRequest,
			fmt.Sprintf("capacidade deve estar entre 1 e %d.", math.MaxInt16))
		return
	}

	t, err := h.repo.Create(r.Context(), name, req.Capacity)
	switch {
	case errors.Is(err, ErrDuplicateName):
		httpx.Error(w, http.StatusConflict, fmt.Sprintf("já existe uma mesa chamada %q.", name))
		return
	case err != nil:
		slog.Error("criando mesa", "erro", err)
		httpx.Error(w, http.StatusInternalServerError, "erro interno.")
		return
	}

	httpx.JSON(w, http.StatusCreated, t)
}

// List godoc
//
//	@Summary		Lista mesas
//	@Description	Lista as mesas. Sem o parâmetro `active`, devolve todas — ativas e inativas.
//	@Tags			mesas
//	@Produce		json
//	@Param			active	query		bool	false	"Filtra por mesas ativas (true) ou inativas (false). Omitido = sem filtro"
//	@Success		200		{array}		Table
//	@Failure		400		{object}	httpx.ErrorResponse	"Parâmetro active não é booleano"
//	@Router			/tables [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	// nil = sem filtro. Só vira ponteiro se o parâmetro veio de fato.
	var active *bool
	if raw := r.URL.Query().Get("active"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "parâmetro 'active' deve ser true ou false.")
			return
		}
		active = &v
	}

	tables, err := h.repo.List(r.Context(), active)
	if err != nil {
		slog.Error("listando mesas", "erro", err)
		httpx.Error(w, http.StatusInternalServerError, "erro interno.")
		return
	}

	httpx.JSON(w, http.StatusOK, tables)
}

// Get godoc
//
//	@Summary	Detalha uma mesa
//	@Tags		mesas
//	@Produce	json
//	@Param		id	path		string	true	"ID da mesa (UUID)"
//	@Success	200	{object}	Table
//	@Failure	400	{object}	httpx.ErrorResponse	"ID não é um UUID"
//	@Failure	404	{object}	httpx.ErrorResponse	"Mesa não encontrada"
//	@Router		/tables/{id} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "id de mesa inválido.")
		return
	}

	t, err := h.repo.GetByID(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "mesa não encontrada.")
		return
	case err != nil:
		slog.Error("buscando mesa", "erro", err, "id", id)
		httpx.Error(w, http.StatusInternalServerError, "erro interno.")
		return
	}

	httpx.JSON(w, http.StatusOK, t)
}

// Ponteiro em todo campo: nil significa "não veio no corpo", e não "veio zero".
// Sem isso, {"is_active": false} seria indistinguível de um PATCH que nem
// mencionou is_active — e desativar mesa é exatamente o caso de uso do PATCH.
type updateRequest struct {
	Name     *string `json:"name"      example:"Mesa 12"`
	Capacity *int    `json:"capacity"  example:"6"`
	IsActive *bool   `json:"is_active" example:"false"`
}

// Update godoc
//
//	@Summary		Atualiza uma mesa
//	@Description	Atualização parcial: só os campos enviados são alterados. Enviar `{"is_active": false}` desativa a mesa.
//	@Tags			mesas
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string			true	"ID da mesa (UUID)"
//	@Param			mesa	body		updateRequest	true	"Campos a alterar (ao menos um)"
//	@Success		200		{object}	Table
//	@Failure		400		{object}	httpx.ErrorResponse	"Corpo vazio, ID inválido ou valor fora do intervalo"
//	@Failure		404		{object}	httpx.ErrorResponse	"Mesa não encontrada"
//	@Failure		409		{object}	httpx.ErrorResponse	"Já existe mesa com o novo nome"
//	@Router			/tables/{id} [patch]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "id de mesa inválido.")
		return
	}

	var req updateRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "corpo da requisição inválido.")
		return
	}

	if req.Name == nil && req.Capacity == nil && req.IsActive == nil {
		httpx.Error(w, http.StatusBadRequest, "nenhum campo para atualizar.")
		return
	}

	params := UpdateParams{Capacity: req.Capacity, IsActive: req.IsActive}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			httpx.Error(w, http.StatusBadRequest, "nome da mesa não pode ser vazio.")
			return
		}
		params.Name = &name
	}
	if req.Capacity != nil && (*req.Capacity <= 0 || *req.Capacity > math.MaxInt16) {
		httpx.Error(w, http.StatusBadRequest,
			fmt.Sprintf("capacidade deve estar entre 1 e %d.", math.MaxInt16))
		return
	}

	t, err := h.repo.Update(r.Context(), id, params)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "mesa não encontrada.")
		return
	case errors.Is(err, ErrDuplicateName):
		httpx.Error(w, http.StatusConflict, "já existe uma mesa com esse nome.")
		return
	case err != nil:
		slog.Error("atualizando mesa", "erro", err, "id", id)
		httpx.Error(w, http.StatusInternalServerError, "erro interno.")
		return
	}

	httpx.JSON(w, http.StatusOK, t)
}
