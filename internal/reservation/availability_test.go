package reservation

import (
	"context"
	"errors"
	"testing"
	"time"
)

// em(20, 19, 30) = 20/07/2026 às 19h30 no fuso de São Paulo.
// O dia é parâmetro porque a última reserva do dia atravessa a meia-noite.
func em(dia, hora, minuto int) time.Time {
	return time.Date(2026, 7, dia, hora, minuto, 0, 0, fusoSP)
}

func jan(iniDia, iniH, iniM, fimDia, fimH, fimM int) Window {
	return Window{StartsAt: em(iniDia, iniH, iniM), EndsAt: em(fimDia, fimH, fimM)}
}

func TestJanelasLivres(t *testing.T) {
	expediente := jan(20, 18, 0, 20, 23, 0) // 18h–23h de 20/07

	casos := []struct {
		nome     string
		ocupadas []Window
		querem   []Window
	}{
		{
			nome:     "sem reservas: o expediente inteiro está livre",
			ocupadas: nil,
			querem:   []Window{jan(20, 18, 0, 20, 23, 0)},
		},
		{
			nome:     "uma reserva no meio: sobra antes e depois",
			ocupadas: []Window{jan(20, 19, 0, 20, 21, 0)},
			querem: []Window{
				jan(20, 18, 0, 20, 19, 0),
				jan(20, 21, 0, 20, 23, 0),
			},
		},
		{
			nome:     "reserva colada na abertura: não sobra janela antes",
			ocupadas: []Window{jan(20, 18, 0, 20, 20, 0)},
			querem:   []Window{jan(20, 20, 0, 20, 23, 0)},
		},
		{
			nome:     "reserva colada no fechamento: não sobra janela depois",
			ocupadas: []Window{jan(20, 21, 0, 20, 23, 0)},
			querem:   []Window{jan(20, 18, 0, 20, 21, 0)},
		},
		{
			nome:     "expediente inteiro tomado: nenhuma janela",
			ocupadas: []Window{jan(20, 18, 0, 20, 23, 0)},
			querem:   []Window{},
		},
		{
			// A última mesa do dia: senta 22h30, sai 00h30. A validação #8
			// permite. Do ponto de vista da janela DESTE dia, ela só bloqueia
			// até as 23h — o recorte tem que cortar o excedente.
			nome:     "reserva transborda o fechamento: recortada em 23h",
			ocupadas: []Window{jan(20, 22, 30, 21, 0, 30)},
			querem:   []Window{jan(20, 18, 0, 20, 22, 30)},
		},
		{
			// Limites [): 19–20 e 20–21 são adjacentes, não sobrepostas. Não pode
			// aparecer uma janela livre de duração zero às 20h.
			nome: "reservas adjacentes: sem janela fantasma de duração zero",
			ocupadas: []Window{
				jan(20, 19, 0, 20, 20, 0),
				jan(20, 20, 0, 20, 21, 0),
			},
			querem: []Window{
				jan(20, 18, 0, 20, 19, 0),
				jan(20, 21, 0, 20, 23, 0),
			},
		},
		{
			nome: "duas reservas com intervalo entre elas",
			ocupadas: []Window{
				jan(20, 19, 0, 20, 20, 0),
				jan(20, 21, 0, 20, 22, 0),
			},
			querem: []Window{
				jan(20, 18, 0, 20, 19, 0),
				jan(20, 20, 0, 20, 21, 0),
				jan(20, 22, 0, 20, 23, 0),
			},
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			got := janelasLivres(expediente, tc.ocupadas)

			if len(got) != len(tc.querem) {
				t.Fatalf("%d janela(s), quero %d\ngot:  %s\nwant: %s",
					len(got), len(tc.querem), fmtJanelas(got), fmtJanelas(tc.querem))
			}
			for i := range got {
				if !got[i].StartsAt.Equal(tc.querem[i].StartsAt) || !got[i].EndsAt.Equal(tc.querem[i].EndsAt) {
					t.Errorf("janela %d = %s, quero %s", i, fmtJanela(got[i]), fmtJanela(tc.querem[i]))
				}
			}
		})
	}
}

// Expediente calculado a partir da data + ServiceHours. ParseInLocation é o que
// impede o dia de nascer três horas deslocado.
func TestExpedienteDe(t *testing.T) {
	s := NewSchedule(nil, expedientePadrao())

	exp, err := s.expedienteDe(context.Background(), "2026-07-20")
	if err != nil {
		t.Fatalf("erro = %v, quero sucesso", err)
	}
	if !exp.StartsAt.Equal(em(20, 18, 0)) || !exp.EndsAt.Equal(em(20, 23, 0)) {
		t.Errorf("expediente = %s, quero 18:00–23:00 no fuso de SP", fmtJanela(exp))
	}

	_, err = s.expedienteDe(context.Background(), "20/07/2026")
	var ve ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("formato inválido devolveu %v (%T), quero ValidationError", err, err)
	}
}

func fmtJanela(w Window) string {
	return w.StartsAt.In(fusoSP).Format("02/01 15:04") + "–" + w.EndsAt.In(fusoSP).Format("02/01 15:04")
}

func fmtJanelas(ws []Window) string {
	if len(ws) == 0 {
		return "[]"
	}
	s := ""
	for _, w := range ws {
		s += "\n  " + fmtJanela(w)
	}
	return s
}
