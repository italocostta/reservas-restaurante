// Package httpx tem os helpers de resposta HTTP compartilhados pelos pacotes de
// domínio. É folha no grafo de imports — só depende da stdlib. Se estes helpers
// morassem em httpserver (como a spec previa), table e reservation precisariam
// importar httpserver, que já importa os dois para montar o router: ciclo, e Go
// recusa compilar.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// errorBody é o formato de erro da v1: só mensagem humana, sem código
// machine-readable. Migrar para código estruturado é breaking change de
// contrato — débito técnico intencional #1 da spec.
type errorBody struct {
	Error string `json:"error"`
}

func JSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// O status já foi enviado — não dá mais para trocar a resposta por um
		// 500. Só resta registrar e deixar o corpo truncado.
		slog.Error("falha ao serializar resposta", "erro", err)
	}
}

func Error(w http.ResponseWriter, status int, message string) {
	JSON(w, status, errorBody{Error: message})
}
