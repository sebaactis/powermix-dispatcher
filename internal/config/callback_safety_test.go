package config_test

import (
	"strings"
	"testing"

	"compras-remotas/internal/config"
)

func configWithPublicBase(t *testing.T, env, baseURL, batidosSecret string) string {
	t.Helper()

	batidos := `"base_url": "https://api.batidos.invalid", "key": "k", "secret": "s"`
	if batidosSecret != "" {
		batidos += `, "callback_secret": "` + batidosSecret + `"`
	}

	base := ""
	if baseURL != "" {
		base = `"public_base_url": "` + baseURL + `",`
	}

	return `{
  "env": "` + env + `",
  ` + base + `
  "batidos": {` + batidos + `},
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
}

func TestConfigRejectsAPlainHTTPCallbackBaseInProduction(t *testing.T) {
	_, err := config.Load(writeConfig(t, configWithPublicBase(t, config.EnvProduction, "http://api.example.com", "ruta-secreta")))
	if err == nil {
		t.Fatalf("el notifyUrl lleva el secreto de callback: en producción no puede viajar sin TLS")
	}

	if !strings.Contains(err.Error(), "https") {
		t.Fatalf("el error debe decir que hace falta https y llegó: %v", err)
	}
}

func TestConfigAcceptsAPlainHTTPCallbackBaseInSandbox(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, configWithPublicBase(t, config.EnvSandbox, "http://127.0.0.1:8080", "ruta-secreta")))
	if err != nil {
		t.Fatalf("en sandbox se permite http para poder probar con un túnel: %v", err)
	}

	if cfg.PublicBaseURL != "http://127.0.0.1:8080" {
		t.Fatalf("la base pública debía quedar cargada y quedó %q", cfg.PublicBaseURL)
	}
}

func TestConfigRejectsACallbackBaseWithoutAHost(t *testing.T) {
	for _, baseURL := range []string{"https://", "no-es-una-url", "ftp://api.example.com"} {
		t.Run(baseURL, func(t *testing.T) {
			if _, err := config.Load(writeConfig(t, configWithPublicBase(t, config.EnvSandbox, baseURL, "ruta-secreta"))); err == nil {
				t.Fatalf("una base pública %q no sirve para armar el notifyUrl y debía rechazarse", baseURL)
			}
		})
	}
}

func TestConfigRejectsACallbackSecretThatBreaksTheRoute(t *testing.T) {
	cases := []struct {
		name   string
		secret string
	}{
		{name: "con barra", secret: "ruta/secreta"},
		{name: "punto suelto", secret: "."},
		{name: "punto punto", secret: ".."},
		{name: "con espacios", secret: "ruta secreta"},
		{name: "con dos puntos", secret: "ruta:secreta"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, configWithPublicBase(t, config.EnvSandbox, "https://api.example.com", testCase.secret)))
			if err == nil {
				t.Fatalf("el secreto %q partiría la ruta del callback y debía rechazarse", testCase.secret)
			}

			if !strings.Contains(err.Error(), "callback_secret") {
				t.Fatalf("el error debe nombrar el campo y llegó: %v", err)
			}
		})
	}
}

func TestConfigAcceptsACallbackSecretMadeOfPathSafeCharacters(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, configWithPublicBase(t, config.EnvProduction, "https://api.example.com", "Kiosco-01_abc.XYZ")))
	if err != nil {
		t.Fatalf("un secreto con letras, números, guiones y punto debe servir: %v", err)
	}

	if cfg.BatidosCallbackSecret != "Kiosco-01_abc.XYZ" {
		t.Fatalf("el secreto debía quedar cargado y quedó %q", cfg.BatidosCallbackSecret)
	}
}
