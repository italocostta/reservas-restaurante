package reservation

import (
	"errors"
	"fmt"
)

var (
	// ErrSlotTaken é sinal INTERNO, não erro de API. O repositório o devolve
	// quando o Postgres recusa o INSERT com 23P01 (exclusion_violation), e é o
	// único ponto do pacote que sabe disso. O allocator o converte em
	// ErrTableUnavailable (caminho manual) ou o usa como gatilho de retry
	// (caminho automático). Nunca deve chegar ao handler.
	ErrSlotTaken = errors.New("horário já ocupado nessa mesa")

	// ErrNoAvailability: a heurística não achou nenhuma mesa livre. → 409
	ErrNoAvailability = errors.New("nenhuma mesa disponível para o horário solicitado")

	// ErrTableUnavailable: a mesa PEDIDA está ocupada nesse horário. → 409
	//
	// Separado de ErrNoAvailability de propósito: quando o staff pediu a Mesa 12
	// explicitamente, responder "sem disponibilidade para o horário" mente sobre
	// a causa — pode haver dez mesas livres, só não aquela.
	ErrTableUnavailable = errors.New("a mesa solicitada já está reservada para esse horário")

	// ErrNotFound: a reserva não existe. → 404
	ErrNotFound = errors.New("reserva não encontrada")
)

// ValidationError carrega a mensagem já pronta para o usuário, com os números do
// caso concreto — "Grupo de 6 pessoas excede a capacidade da mesa (4)."
//
// Por que um TIPO e não mais uma sentinela: sentinela responde "qual condição",
// nunca "com quais valores". Daria para fazer fmt.Errorf("%w: grupo de %d...",
// ErrValidacao, n) — o errors.Is continuaria funcionando — mas o Error() sairia
// com o texto da sentinela grudado na frente ("dados inválidos: grupo de 6..."),
// e o handler teria que recortar string para montar a resposta. O tipo devolve a
// mensagem limpa, e o handler a repassa inteira. → 400
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string { return e.Message }

// invalido é o construtor usado pelas validações do allocator.
func invalido(formato string, args ...any) error {
	return ValidationError{Message: fmt.Sprintf(formato, args...)}
}
