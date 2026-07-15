// Package settings guarda a configuração do restaurante que passou a ser editável
// em runtime: o expediente (horário + fuso), quais dias da semana ele opera, e as
// exceções por data (feriados fechados, aberturas especiais).
//
// Até a migration 0009 isso vinha do .env, imutável. Agora vem do banco — e a
// razão de existir do pacote é que o allocator e a agenda RESPEITEM essa config,
// não só a exibam. Uma UI que diz "fechado" enquanto o backend aceita reserva é a
// mentira silenciosa que o resto do projeto combate.
package settings

import (
	"time"

	"reservas-restaurante/internal/reservation"
)

// Settings é a config vigente do restaurante, já no formato do domínio.
//
// Hours reusa reservation.ServiceHours — é o mesmo conceito (início/fim/fuso) que
// o allocator e a agenda já consomem, e duplicá-lo criaria duas verdades sobre o
// que é "expediente".
type Settings struct {
	Hours reservation.ServiceHours

	// Dias da semana em que o restaurante opera, na convenção do time.Weekday do
	// Go (Sunday=0 … Saturday=6) — que por sorte é a MESMA do EXTRACT(DOW) do
	// Postgres, então não há conversão entre banco e código.
	OpenWeekdays map[time.Weekday]bool
}

// Exception é uma data que foge da regra semanal. IsOpen diz se é uma abertura
// especial (num dia normalmente fechado) ou um fechamento (num dia normalmente
// aberto).
type Exception struct {
	Day    string `json:"day"     example:"2026-12-25"` // AAAA-MM-DD
	IsOpen bool   `json:"is_open" example:"false"`
	Note   string `json:"note"    example:"Natal"`
}

// AbertoEm decide se o restaurante opera numa data — a única lógica de negócio de
// verdade deste pacote, e por isso a única com teste de unidade.
//
// A precedência importa: a EXCEÇÃO manda sobre a regra semanal. "Fechamos às
// segundas, mas nesta segunda vamos abrir para um evento" só se expressa se a
// exceção do dia vencer o `open_weekdays`. É o mesmo princípio da exceção vencer o
// padrão em qualquer sistema de calendário.
//
// `dia` vem como "AAAA-MM-DD" e é interpretado NO FUSO DO RESTAURANTE — a mesma
// armadilha de sempre: o dia da semana de um instante depende do fuso, e é o dia
// de parede do restaurante que decide se ele abre.
func AbertoEm(s Settings, excecoes map[string]Exception, dia string) (bool, error) {
	d, err := time.ParseInLocation(time.DateOnly, dia, s.Hours.TZ)
	if err != nil {
		return false, reservation.ValidationError{Message: "Data inválida (esperado AAAA-MM-DD)."}
	}

	if ex, tem := excecoes[dia]; tem {
		return ex.IsOpen, nil
	}

	return s.OpenWeekdays[d.Weekday()], nil
}
