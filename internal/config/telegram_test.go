package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTelegramConfig(t *testing.T, env string, telegram map[string]string) string {
	t.Helper()

	file := map[string]any{
		"env": env,
		"batidos": map[string]string{
			"base_url": "https://api.batidos.test",
			"key":      "k",
			"secret":   "s",
		},
		"snacks": map[string]string{
			"access_key": "ak",
			"app_secret": "as",
		},
		"mercadopago": fullMP(),
		"kiosks": []map[string]any{{
			"id":        "kiosk-1",
			"mp_pos_id": "POS1",
			"machines": []map[string]string{
				{"type": "batidos", "device_id": "d", "device_no": "n", "device_type_id": "t"},
				{"type": "snacks", "mid": "m"},
			},
		}},
	}

	if env == EnvProduction {
		file["public_base_url"] = "https://api.example.test"
	}

	if telegram != nil {
		file["telegram"] = telegram
	}

	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func clearTelegramEnv(t *testing.T) {
	t.Helper()

	clearMPEnv(t)
	for _, name := range []string{"TELEGRAM_BASE_URL", "TELEGRAM_BOT_TOKEN", "TELEGRAM_CHAT_ID"} {
		t.Setenv(name, "")
	}
}

func TestTelegramOffInSandboxLeavesAlertsInTheLog(t *testing.T) {
	clearTelegramEnv(t)

	cfg, err := Load(writeTelegramConfig(t, EnvSandbox, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.TelegramEnabled() {
		t.Fatal("sin token Telegram no debería estar prendido")
	}
}

func TestTelegramFullConfigLoadsWithDefaultURL(t *testing.T) {
	clearTelegramEnv(t)

	cfg, err := Load(writeTelegramConfig(t, EnvProduction, map[string]string{"bot_token": "123:abc", "chat_id": "-100"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.TelegramEnabled() {
		t.Fatal("con token Telegram debería estar prendido")
	}

	if cfg.TelegramBaseURL != TelegramURL {
		t.Fatalf("base url = %q, quería %q", cfg.TelegramBaseURL, TelegramURL)
	}

	if cfg.TelegramBotToken != "123:abc" || cfg.TelegramChatID != "-100" {
		t.Fatalf("no se tomaron los valores del archivo: token=%q chat=%q", cfg.TelegramBotToken, cfg.TelegramChatID)
	}
}

func TestTelegramEnvOverridesFile(t *testing.T) {
	clearTelegramEnv(t)
	t.Setenv("TELEGRAM_BOT_TOKEN", " 999:env ")
	t.Setenv("TELEGRAM_CHAT_ID", "-200")
	t.Setenv("TELEGRAM_BASE_URL", "http://127.0.0.1:9405/")

	cfg, err := Load(writeTelegramConfig(t, EnvSandbox, map[string]string{"bot_token": "123:file", "chat_id": "-100"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.TelegramBotToken != "999:env" || cfg.TelegramChatID != "-200" {
		t.Fatalf("no se tomaron las variables de entorno: token=%q chat=%q", cfg.TelegramBotToken, cfg.TelegramChatID)
	}

	if cfg.TelegramBaseURL != "http://127.0.0.1:9405" {
		t.Fatalf("base url = %q, quería sin barra final", cfg.TelegramBaseURL)
	}
}

func TestTelegramConfigProblems(t *testing.T) {
	cases := []struct {
		name     string
		env      string
		telegram map[string]string
		want     string
	}{
		{"producción sin Telegram", EnvProduction, nil, "en producción los avisos por Telegram son obligatorios"},
		{"chat sin token", EnvSandbox, map[string]string{"chat_id": "-100"}, "configurado a medias"},
		{"token sin chat", EnvSandbox, map[string]string{"bot_token": "123:abc"}, "TELEGRAM_CHAT_ID"},
		{"base url relativa", EnvSandbox, map[string]string{"bot_token": "123:abc", "chat_id": "-100", "base_url": "api"}, "telegram.base_url"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearTelegramEnv(t)

			_, err := Load(writeTelegramConfig(t, tc.env, tc.telegram))
			if err == nil {
				t.Fatalf("Load no falló; quería un error con %q", tc.want)
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v; quería que mencione %q", err, tc.want)
			}

			if strings.Contains(err.Error(), "123:abc") {
				t.Fatalf("el error filtra el token: %v", err)
			}
		})
	}
}
