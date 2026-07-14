package notification

import (
	"context"
	"log/slog"
	"strings"
)

// Sender é declarado aqui, pelo consumidor (o worker), não pelo provedor.
// Mesmo princípio de todo o projeto: quem usa define o contrato.
//
// Um Sender DEVE respeitar o ctx: ele carrega o timeout de cada envio e o
// cancelamento do shutdown. Um Sender que ignora o ctx trava o encerramento do
// processo inteiro.
type Sender interface {
	Send(ctx context.Context, n Notification) error
}

// LogSender é o único Sender que este projeto tem. Notificação de verdade
// (email/SMS) está explicitamente fora de escopo (seção 1 da spec), e trocá-lo
// por um cliente de Twilio é implementar UMA função.
//
// O que a Fase 2 existe para ensinar é a mecânica — outbox, fila, pool,
// shutdown gracioso — e ela não fica mais interessante com um provedor real
// atrás. Fica só mais cara de testar.
type LogSender struct{}

func (LogSender) Send(ctx context.Context, n Notification) error {
	slog.InfoContext(ctx, "notificação enviada",
		"tipo", n.Kind,
		"reserva", n.ReservationID,
		"cliente", n.Payload.CustomerName,
		"telefone", mascarar(n.Payload.CustomerPhone),
		"tentativa", n.Attempts,
	)

	return nil
}

// Log é dado que vaza: vai para arquivo, para o stdout de um container, para um
// agregador de terceiros. Telefone de cliente não tem por que estar inteiro em
// nenhum desses lugares — os quatro últimos dígitos bastam para correlacionar um
// problema, e o resto é passivo de privacidade sem contrapartida.
func mascarar(telefone string) string {
	if len(telefone) <= 4 {
		return strings.Repeat("*", len(telefone))
	}
	return strings.Repeat("*", len(telefone)-4) + telefone[len(telefone)-4:]
}
