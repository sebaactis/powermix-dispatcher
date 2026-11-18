package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMPConfig(t *testing.T, env string, mp map[string]string, posID string) string {
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
		"kiosks": []map[string]any{{
			"id":        "kiosk-1",
			"mp_pos_id": posID,
			"machines": []map[string]string{
				{"type": "batidos", "device_id": "d", "device_no": "n", "device_type_id": "t"},
				{"type": "snacks", "mid": "m"},
			},
		}},
	}

	if env == EnvProduction {
		file["public_base_url"] = "https://api.example.test"
	}

	if mp != nil {
		file["mercadopago"] = mp
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

func clearMPEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{"MP_BASE_URL", "MP_ACCESS_TOKEN", "MP_WEBHOOK_SECRET", "MP_CALLBACK_SECRET"} {
		t.Setenv(name, "")
	}
}

func fullMP() map[string]string {
	return map[string]string{
		"access_token":    "TEST-token",
		"webhook_secret":  "whsec",
		"callback_secret": "mp-callback",
	}
}

func TestMercadoPagoOffInSandboxKeepsSimulatedPayment(t *testing.T) {
	clearMPEnv(t)

	cfg, err := Load(writeMPConfig(t, EnvSandbox, nil, ""))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.MercadoPagoEnabled() {
		t.Fatal("sin access token Mercado Pago no debería estar prendido")
	}
}

func TestMercadoPagoFullConfigLoadsWithDefaultURL(t *testing.T) {
	clearMPEnv(t)

	cfg, err := Load(writeMPConfig(t, EnvSandbox, fullMP(), "POS1"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.MercadoPagoEnabled() {
		t.Fatal("con access token Mercado Pago debería estar prendido")
	}

	if cfg.MercadoPagoBaseURL != MercadoPagoURL {
		t.Fatalf("base url = %q, quería %q", cfg.MercadoPagoBaseURL, MercadoPagoURL)
	}

	if cfg.Kiosks[0].MPPosID != "POS1" {
		t.Fatalf("mp_pos_id = %q", cfg.Kiosks[0].MPPosID)
	}
}

func TestMercadoPagoEnvOverridesFile(t *testing.T) {
	clearMPEnv(t)
	t.Setenv("MP_ACCESS_TOKEN", "TEST-env")
	t.Setenv("MP_WEBHOOK_SECRET", "env-wh")
	t.Setenv("MP_CALLBACK_SECRET", "env-cb")
	t.Setenv("MP_BASE_URL", "http://127.0.0.1:9404/")

	cfg, err := Load(writeMPConfig(t, EnvSandbox, nil, "POS1"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.MercadoPagoAccessToken != "TEST-env" || cfg.MercadoPagoWebhookSecret != "env-wh" || cfg.MercadoPagoCallbackSecret != "env-cb" {
		t.Fatalf("no se tomaron las variables de entorno: %+v", cfg)
	}

	if cfg.MercadoPagoBaseURL != "http://127.0.0.1:9404" {
		t.Fatalf("base url = %q, quería sin barra final", cfg.MercadoPagoBaseURL)
	}
}

func TestMercadoPagoConfigProblems(t *testing.T) {
	cases := []struct {
		name  string
		env   string
		mp    map[string]string
		posID string
		want  string
	}{
		{"producción sin Mercado Pago", EnvProduction, nil, "POS1", "en producción Mercado Pago es obligatorio"},
		{"secretos sin token", EnvSandbox, map[string]string{"webhook_secret": "w"}, "", "configurado a medias"},
		{"sin secreto de webhook", EnvSandbox, map[string]string{"access_token": "t", "callback_secret": "cb"}, "POS1", "MP_WEBHOOK_SECRET"},
		{"sin secreto de ruta", EnvSandbox, map[string]string{"access_token": "t", "webhook_secret": "w"}, "POS1", "MP_CALLBACK_SECRET"},
		{"secreto de ruta inválido", EnvSandbox, map[string]string{"access_token": "t", "webhook_secret": "w", "callback_secret": "a/b"}, "POS1", "mercadopago.callback_secret"},
		{"base url relativa", EnvSandbox, map[string]string{"access_token": "t", "webhook_secret": "w", "callback_secret": "cb", "base_url": "api"}, "POS1", "mercadopago.base_url"},
		{"mueble sin caja", EnvSandbox, fullMP(), "", "falta mp_pos_id"},
		{"producción sin caja", EnvProduction, fullMP(), "", "falta mp_pos_id"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearMPEnv(t)

			_, err := Load(writeMPConfig(t, tc.env, tc.mp, tc.posID))
			if err == nil {
				t.Fatalf("Load no falló; quería un error con %q", tc.want)
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v; quería que mencione %q", err, tc.want)
			}
		})
	}
}
