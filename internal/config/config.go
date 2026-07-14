package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	// Embute a base IANA de fusos horários no binário (~450 KB). Sem isto,
	// time.LoadLocation("America/Sao_Paulo") FALHA no Windows, que não tem
	// base IANA nativa: o Go cai no zoneinfo.zip do GOROOT, que existe na sua
	// máquina de dev e desaparece no minuto em que você mover o binário.
	_ "time/tzdata"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL       string
	Port              string
	CORSAllowedOrigin string
	Env               string

	// Duração desde a meia-noite no fuso ServiceTZ: "18:00" vira 18h.
	// Não é time.Time porque hora-do-dia não tem data — e um time.Time
	// obrigaria a inventar uma.
	ServiceStart time.Duration
	ServiceEnd   time.Duration
	ServiceTZ    *time.Location
}

// Load lê e valida o ambiente inteiro de uma vez, no boot. Ou devolve um
// Config íntegro, ou devolve erro e o processo não sobe. Nenhum os.Getenv
// espalhado pelo resto do código: variável faltando vira erro de partida,
// nunca um comportamento estranho na primeira requisição que passar por ali.
func Load() (Config, error) {
	// .env ausente não é erro — em produção as variáveis vêm do ambiente.
	// Qualquer OUTRA falha de leitura (permissão, sintaxe) é.
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("lendo .env: %w", err)
	}

	cfg := Config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		Port:              envOr("PORT", "8080"),
		CORSAllowedOrigin: envOr("CORS_ALLOWED_ORIGIN", "http://localhost:5173"),
		Env:               envOr("ENV", "development"),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL é obrigatória")
	}
	if _, err := strconv.Atoi(cfg.Port); err != nil {
		return Config{}, fmt.Errorf("PORT deve ser numérica, veio %q", cfg.Port)
	}

	tzName := envOr("SERVICE_TZ", "America/Sao_Paulo")
	tz, err := time.LoadLocation(tzName)
	if err != nil {
		return Config{}, fmt.Errorf("SERVICE_TZ inválida (%q): %w", tzName, err)
	}
	cfg.ServiceTZ = tz

	if cfg.ServiceStart, err = parseHHMM(envOr("SERVICE_START", "18:00")); err != nil {
		return Config{}, fmt.Errorf("SERVICE_START: %w", err)
	}
	if cfg.ServiceEnd, err = parseHHMM(envOr("SERVICE_END", "23:00")); err != nil {
		return Config{}, fmt.Errorf("SERVICE_END: %w", err)
	}
	if cfg.ServiceStart >= cfg.ServiceEnd {
		return Config{}, fmt.Errorf(
			"SERVICE_START (%v) deve ser anterior a SERVICE_END (%v)",
			cfg.ServiceStart, cfg.ServiceEnd)
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseHHMM converte "18:00" na duração desde a meia-noite.
func parseHHMM(s string) (time.Duration, error) {
	hh, mm, found := strings.Cut(strings.TrimSpace(s), ":")
	if !found {
		return 0, fmt.Errorf("formato deve ser HH:MM, veio %q", s)
	}

	hours, err := strconv.Atoi(hh)
	if err != nil || hours < 0 || hours > 23 {
		return 0, fmt.Errorf("hora inválida em %q", s)
	}
	mins, err := strconv.Atoi(mm)
	if err != nil || mins < 0 || mins > 59 {
		return 0, fmt.Errorf("minuto inválido em %q", s)
	}

	return time.Duration(hours)*time.Hour + time.Duration(mins)*time.Minute, nil
}
