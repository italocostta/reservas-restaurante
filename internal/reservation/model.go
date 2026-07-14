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

// Reservation espelha uma linha de reservations.
type Reservation struct {
	ID            uuid.UUID `json:"id"`
	TableID       uuid.UUID `json:"table_id"`
	CustomerName  string    `json:"customer_name"  example:"Maria Silva"`
	CustomerPhone string    `json:"customer_phone" example:"11999998888"`
	PartySize     int       `json:"party_size"     example:"4"`
	StartsAt      time.Time `json:"starts_at"`
	EndsAt        time.Time `json:"ends_at"`
	Status        Status    `json:"status"         example:"confirmed"`
	CreatedAt     time.Time `json:"created_at"`
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
// PreferredTableID é ponteiro porque nil é a informação: significa "escolha uma
// mesa por mim" (heurística gulosa, Fase 1b). Preenchido, significa "use esta
// mesa" — e é o que decide se o retry da Fase 1c faz sentido ou não.
type AllocationRequest struct {
	PreferredTableID *uuid.UUID
	CustomerName     string
	CustomerPhone    string
	PartySize        int
	StartsAt         time.Time
	EndsAt           time.Time
}
