package config_test

import (
	"testing"

	"compras-remotas/internal/config"
)

const publicBaseConfig = `{
  "env": "sandbox",
  "public_base_url": "https://kiosco.example.test/",
  "batidos": {
    "base_url": "https://api.batidos.invalid",
    "key": "k",
    "secret": "s",
    "callback_secret": "ruta-batidos"
  },
  "snacks": {
    "base_url": "https://api.snacks.invalid",
    "access_key": "ak",
    "app_secret": "as"
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

func TestConfigReadsThePublicBaseURL(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, publicBaseConfig))
	if err != nil {
		t.Fatalf("la configuración debía cargar: %v", err)
	}

	if cfg.PublicBaseURL != "https://kiosco.example.test" {
		t.Fatalf("se esperaba la base pública del archivo sin la barra final y llegó %q", cfg.PublicBaseURL)
	}
}

func TestConfigTakesThePublicBaseURLFromTheEnvironment(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "  https://otro.example.test/  ")

	cfg, err := config.Load(writeConfig(t, publicBaseConfig))
	if err != nil {
		t.Fatalf("la configuración debía cargar: %v", err)
	}

	if cfg.PublicBaseURL != "https://otro.example.test" {
		t.Fatalf("el entorno debe ganarle al archivo, sin espacios ni barra final, y llegó %q", cfg.PublicBaseURL)
	}
}

func TestConfigLeavesThePublicBaseURLEmptyWhenAbsent(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, callbackConfig))
	if err != nil {
		t.Fatalf("la configuración sin base pública debía cargar: %v", err)
	}

	if cfg.PublicBaseURL != "" {
		t.Fatalf("sin base pública declarada el campo queda vacío y llegó %q", cfg.PublicBaseURL)
	}
}
