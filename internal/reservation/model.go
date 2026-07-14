package reservation

import (
	"time"

	"github.com/google/uuid"
)

// Status é um tipo próprio, não uma string solta. Go não tem enum; o idioma é
// um tipo nomeado com constantes. Isso faz `res.Status = "confirmd"` não
// compilar — o typo vira erro de build em vez de linha inválida no banco.
// Os dois valores espelham o CHECK (status IN ('confirmed','cancelled')).
type Status string

const (
	StatusConfirmed Status = "confirmed"
	StatusCancelled Status = "cancelled"
)

// Reservation espelha uma reserva e as mesas que ela ocupa.
//
// TableIDs é uma LISTA desde a Fase 3a: uma reserva pode ocupar mais de uma mesa
// (o staff empurra duas de 4 para sentar um grupo de 8). Antes era um uuid.UUID
// único — mudança quebrada de contrato, feita agora justamente porque o frontend
// ainda não existe e este é o momento mais barato que vai existir.
type Reservation struct {
	ID            uuid.UUID   `json:"id"`
	TableIDs      []uuid.UUID `json:"table_ids"`
	CustomerName  string      `json:"customer_name"  example:"Maria Silva"`
	CustomerPhone string      `json:"customer_phone" example:"11999998888"`
	PartySize     int         `json:"party_size"     example:"4"`
	StartsAt      time.Time   `json:"starts_at"`
	EndsAt        time.Time   `json:"ends_at"`
	Status        Status      `json:"status"         example:"confirmed"`
	CreatedAt     time.Time   `json:"created_at"`
}

// AllocationRequest é a entrada de CreateReservation — o pedido já parseado e
// validado sintaticamente pelo handler.
//
// Repare que ela NÃO tem tags `json`, e isso é deliberado: o corpo da
// requisição HTTP é outro tipo, não exportado, que vive no handler.go. São
// contratos diferentes com ciclos de vida diferentes — o da API muda quando o
// frontend precisa, o do domínio muda quando a regra de negócio muda. Decodificar
// o JSON direto aqui amarraria os dois e faria a mudança de um forçar a do outro.
//
// PreferredTableIDs tem três significados, e é o que decide todo o resto:
//
//	 vazio  → "escolha a mesa por mim"  → heurística gulosa + retry (Fase 1b/1c)
//	1 mesa  → "use esta mesa"           → caminho manual, sem retry
//	2+ mesas → "empurrei estas mesas"   → COMBINAÇÃO (Fase 3a), sem retry
//
// Repare que aqui NÃO tem ponteiro, ao contrário de todos os outros campos
// opcionais deste projeto. Um slice já tem a representação de "ausente" embutida:
// o zero value dele é nil, e `len(nil) == 0`. O *bool precisou de ponteiro porque
// o zero value de bool (false) é um valor VÁLIDO e legítimo — não dá para
// distinguir "não informado" de "informado como false". Slice não tem esse
// problema, e forçar um ponteiro aqui seria cerimônia sem ganho.
type AllocationRequest struct {
	PreferredTableIDs []uuid.UUID
	CustomerName      string
	CustomerPhone     string
	PartySize         int
	StartsAt          time.Time
	EndsAt            time.Time
}
