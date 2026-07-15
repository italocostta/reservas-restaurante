package settings

import (
	"testing"
	"time"

	_ "time/tzdata"

	"reservas-restaurante/internal/reservation"
)

func fusoSP(t *testing.T) *time.Location {
	t.Helper()
	tz, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	return tz
}

func settingsCom(t *testing.T, dias ...time.Weekday) Settings {
	abertos := map[time.Weekday]bool{}
	for _, d := range dias {
		abertos[d] = true
	}
	return Settings{
		Hours:        reservation.ServiceHours{Start: 18 * time.Hour, End: 23 * time.Hour, TZ: fusoSP(t)},
		OpenWeekdays: abertos,
	}
}

func TestAbertoEm(t *testing.T) {
	// 2026-08-03 é uma SEGUNDA; 2026-08-04, uma TERÇA (no fuso de SP).
	segunda := "2026-08-03"
	terca := "2026-08-04"

	t.Run("dia da semana aberto", func(t *testing.T) {
		s := settingsCom(t, time.Monday, time.Tuesday)
		aberto, err := AbertoEm(s, nil, segunda)
		if err != nil || !aberto {
			t.Errorf("segunda com Monday aberto: aberto=%v err=%v", aberto, err)
		}
	})

	t.Run("dia da semana fechado", func(t *testing.T) {
		s := settingsCom(t, time.Tuesday) // só terça abre
		aberto, err := AbertoEm(s, nil, segunda)
		if err != nil || aberto {
			t.Errorf("segunda fechada: aberto=%v err=%v", aberto, err)
		}
	})

	t.Run("exceção FECHA um dia normalmente aberto", func(t *testing.T) {
		s := settingsCom(t, time.Monday, time.Tuesday)
		excecoes := map[string]Exception{segunda: {Day: segunda, IsOpen: false, Note: "Feriado"}}

		aberto, err := AbertoEm(s, excecoes, segunda)
		if err != nil || aberto {
			t.Errorf("exceção deveria fechar a segunda: aberto=%v err=%v", aberto, err)
		}
		// A terça, sem exceção, continua seguindo a regra semanal.
		if a, _ := AbertoEm(s, excecoes, terca); !a {
			t.Error("a terça não deveria ter sido afetada pela exceção da segunda")
		}
	})

	t.Run("exceção ABRE um dia normalmente fechado", func(t *testing.T) {
		s := settingsCom(t, time.Tuesday) // segunda fechada pela regra
		excecoes := map[string]Exception{segunda: {Day: segunda, IsOpen: true, Note: "Evento"}}

		aberto, err := AbertoEm(s, excecoes, segunda)
		if err != nil || !aberto {
			t.Errorf("exceção deveria abrir a segunda: aberto=%v err=%v", aberto, err)
		}
	})

	t.Run("data malformada vira ValidationError", func(t *testing.T) {
		s := settingsCom(t, time.Monday)
		_, err := AbertoEm(s, nil, "03/08/2026")
		var ve reservation.ValidationError
		if !errorsAs(err, &ve) {
			t.Errorf("erro = %v (%T), quero ValidationError", err, err)
		}
	})
}

// errorsAs local para não importar "errors" só por uma linha.
func errorsAs(err error, target *reservation.ValidationError) bool {
	ve, ok := err.(reservation.ValidationError)
	if ok {
		*target = ve
	}
	return ok
}
