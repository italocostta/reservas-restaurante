package httpserver

import (
	"fmt"
	"net/http"
	"time"

	"reservas-restaurante/internal/config"
	"reservas-restaurante/internal/httpx"
)

// ServiceHours é o expediente do restaurante, do jeito que o frontend precisa
// ler: strings, não time.Duration.
//
// O nome da rota é /service-hours e não /config de propósito. "Config" é um
// balde: no dia em que alguém precisar expor mais uma coisa, ela vai parar lá
// dentro sem discussão, e um balde chamado /config acaba com um campo a mais
// que ninguém devia ver. Este endpoint responde UMA pergunta — "que horas o
// restaurante abre?" — e o nome dele não deixa cabir outra.
type ServiceHours struct {
	Start string `json:"start" example:"18:00"`
	End   string `json:"end"   example:"23:00"`
	TZ    string `json:"tz"    example:"America/Sao_Paulo"`
}

// serviceHours responde o expediente lido do ambiente no boot.
//
// Sem isto, o Vue teria que guardar 18:00/23:00/America-Sao_Paulo num .env
// próprio — a mesma verdade escrita em dois lugares, e nenhum teste vigiando a
// divergência. No dia em que o restaurante passasse a abrir às 17h, o backend
// aceitaria a reserva das 17h e a UI não teria onde desenhá-la. É exatamente o
// drift que o teste de contrato existe para impedir na direção da documentação,
// só que na direção do frontend.
//
//	@Summary		Expediente do restaurante
//	@Description	Horário de funcionamento e fuso usados por toda a API: a validação de `starts_at` na criação de reserva e o cálculo das janelas livres. O frontend lê daqui em vez de guardar uma cópia.
//	@Tags			restaurante
//	@Produce		json
//	@Success		200	{object}	ServiceHours
//	@Router			/service-hours [get]
func serviceHours(cfg config.Config) http.HandlerFunc {
	// Montado UMA vez, no boot, e capturado pela closure: a resposta não muda
	// entre requisições porque a config não muda depois do Load().
	corpo := ServiceHours{
		Start: hhmm(cfg.ServiceStart),
		End:   hhmm(cfg.ServiceEnd),
		TZ:    cfg.ServiceTZ.String(),
	}

	return func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, corpo)
	}
}

// hhmm é o inverso do parseHHMM do config: a duração desde a meia-noite volta a
// ser "18:00". O ida-e-volta existe porque o domínio quer aritmética (somar ao
// início do dia) e o frontend quer um rótulo — e nenhum dos dois deve se
// dobrar ao formato do outro.
func hhmm(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(d.Hours()), int(d.Minutes())%60)
}
