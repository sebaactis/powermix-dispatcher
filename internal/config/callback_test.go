package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"compras-remotas/internal/config"
)

const callbackConfig = `{
  "env": "sandbox",
  "batidos": {
    "base_url": "https://api.batidos.invalid",
    "key": "k",
    "secret": "s",
    "callback_secret": "ruta-batidos"
  },
  "snacks": {
    "base_url": "https://api.snacks.invalid",
    "access_key": "ak",
    "app_secret": "as",
    "callback_secret": "ruta-snacks"
  },
  "kiosks": [
    {
      "id": "kiosco-demo",
      "name": "Mueble demo",
      "machines": [
        {"type": "batidos", "name": "Batidos", "device_id": "d1", "device_no": "n1", "device_type_id": "t1"},
        {"type": "snacks", "name": "Snacks", "mid": "M1"}
      ]
    }
  ]
}`

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("no se pudo escribir la configuración de prueba: %v", err)
	}

	return path
}

func TestConfigReadsTheCallbackSecrets(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, callbackConfig))
	if err != nil {
		t.Fatalf("la configuración debía cargar: %v", err)
	}

	if cfg.BatidosCallbackSecret != "ruta-batidos" {
		t.Fatalf("se esperaba la ruta secreta de batidos del archivo y llegó %q", cfg.BatidosCallbackSecret)
	}

	if cfg.SnacksCallbackSecret != "ruta-snacks" {
		t.Fatalf("se esperaba la ruta secreta de snacks del archivo y llegó %q", cfg.SnacksCallbackSecret)
	}
}

func TestConfigTakesTheCallbackSecretsFromTheEnvironment(t *testing.T) {
	t.Setenv("BATIDOS_CALLBACK_SECRET", "ruta-por-entorno")
	t.Setenv("SNACKS_CALLBACK_SECRET", "  ruta-snacks-por-entorno  ")

	cfg, err := config.Load(writeConfig(t, callbackConfig))
	if err != nil {
		t.Fatalf("la configuración debía cargar: %v", err)
	}

	if cfg.BatidosCallbackSecret != "ruta-por-entorno" {
		t.Fatalf("el entorno debe ganarle al archivo y llegó %q", cfg.BatidosCallbackSecret)
	}

	if cfg.SnacksCallbackSecret != "ruta-snacks-por-entorno" {
		t.Fatalf("el entorno debe ganarle al archivo, sin espacios, y llegó %q", cfg.SnacksCallbackSecret)
	}
}

func TestConfigLeavesTheCallbackSecretsEmptyWhenAbsent(t *testing.T) {
	without := `{
  "env": "sandbox",
  "batidos": {"base_url": "https://api.batidos.invalid", "key": "k", "secret": "s"},
  "snacks": {"base_url": "https://api.snacks.invalid", "access_key": "ak", "app_secret": "as"},
  "kiosks": [
    {
      "id": "kiosco-demo",
      "name": "Mueble demo",
      "machines": [
        {"type": "batidos", "name": "Batidos", "device_id": "d1", "device_no": "n1", "device_type_id": "t1"},
        {"type": "snacks", "name": "Snacks", "mid": "M1"}
      ]
    }
  ]
}`

	cfg, err := config.Load(writeConfig(t, without))
	if err != nil {
		t.Fatalf("la configuración debía cargar: %v", err)
	}

	if cfg.BatidosCallbackSecret != "" || cfg.SnacksCallbackSecret != "" {
		t.Fatalf("sin secreto declarado los callbacks quedan cerrados: %q %q", cfg.BatidosCallbackSecret, cfg.SnacksCallbackSecret)
	}
}
