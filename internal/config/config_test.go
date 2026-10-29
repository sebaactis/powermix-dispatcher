package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `{
  "env": "sandbox",
  "batidos": { "base_url": "https://batidos.example.test", "key": "file-key", "secret": "file-secret" },
  "snacks": { "access_key": "file-ak", "app_secret": "file-app-secret" },
  "kiosks": [
    {
      "id": "kiosk-1",
      "name": "Mueble 1",
      "machines": [
        { "type": "batidos", "name": "Batidos", "device_id": "dev-1", "device_no": "devno-1", "device_type_id": "dt-1" },
        { "type": "snacks", "name": "Snacks", "mid": "mid-1" }
      ]
    }
  ]
}`

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadAppliesSandboxDefaultAndEnvSecrets(t *testing.T) {
	t.Setenv("SNACKS_APP_SECRET", "env-app-secret")
	t.Setenv("BATIDOS_SECRET", "env-secret")

	cfg, err := Load(writeTempConfig(t, validConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.SnacksBaseURL != "http://47.110.236.144:5130" {
		t.Fatalf("snacks base url = %q, want the documented sandbox", cfg.SnacksBaseURL)
	}
	if cfg.SnacksAppSecret != "env-app-secret" {
		t.Fatalf("snacks secret = %q, want the env override", cfg.SnacksAppSecret)
	}
	if cfg.BatidosSecret != "env-secret" {
		t.Fatalf("batidos secret = %q, want the env override", cfg.BatidosSecret)
	}
	if cfg.BatidosKey != "file-key" {
		t.Fatalf("batidos key = %q, want the value from the file", cfg.BatidosKey)
	}
	if len(cfg.Kiosks) != 1 || cfg.Kiosks[0].Machines[1].Type != MachineSnacks {
		t.Fatalf("kiosks decoded incorrectly: %+v", cfg.Kiosks)
	}
}

func TestLoadUsesProductionURLWhenEnvironmentIsProduction(t *testing.T) {
	body := strings.Replace(validConfig, `"env": "sandbox"`, `"env": "production"`, 1)

	cfg, err := Load(writeTempConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Host de producción tal como figura en la documentación del proveedor.
	if cfg.SnacksBaseURL != "https://ysopenapi.ourvend.com" {
		t.Fatalf("snacks base url = %q, se esperaba el host de producción documentado", cfg.SnacksBaseURL)
	}
}

func TestLoadRejectsUnknownEnvironment(t *testing.T) {
	body := strings.Replace(validConfig, `"env": "sandbox"`, `"env": "staging"`, 1)

	if _, err := Load(writeTempConfig(t, body)); err == nil {
		t.Fatal("Load accepted an unknown environment")
	}
}

func TestLoadRejectsBatidosWithoutBaseURL(t *testing.T) {
	body := strings.Replace(validConfig, `"base_url": "https://batidos.example.test", `, "", 1)

	_, err := Load(writeTempConfig(t, body))
	if err == nil {
		t.Fatal("Load accepted a configuration without the Batidos API domain")
	}
	if !strings.Contains(err.Error(), "batidos.base_url") {
		t.Fatalf("error = %q, want it to name batidos.base_url", err)
	}
}

func TestLoadRejectsKioskWithoutBothMachines(t *testing.T) {
	body := `{
      "env": "sandbox",
      "batidos": { "base_url": "https://batidos.example.test", "key": "k", "secret": "s" },
      "snacks": { "access_key": "ak", "app_secret": "as" },
      "kiosks": [ { "id": "kiosk-1", "name": "Mueble 1", "machines": [
        { "type": "snacks", "name": "Snacks", "mid": "mid-1" }
      ] } ]
    }`

	_, err := Load(writeTempConfig(t, body))
	if err == nil {
		t.Fatal("Load accepted a kiosk without a batidos machine")
	}
	if !strings.Contains(err.Error(), "kiosk-1") {
		t.Fatalf("error = %q, want it to name the offending kiosk", err)
	}
}

func TestLoadRejectsDuplicateKioskIDs(t *testing.T) {
	body := strings.Replace(validConfig, `"kiosks": [`, `"kiosks": [
    {
      "id": "kiosk-1",
      "name": "Duplicado",
      "machines": [
        { "type": "batidos", "name": "Batidos", "device_id": "d", "device_no": "n", "device_type_id": "t" },
        { "type": "snacks", "name": "Snacks", "mid": "m" }
      ]
    },`, 1)

	if _, err := Load(writeTempConfig(t, body)); err == nil {
		t.Fatal("Load accepted two kiosks with the same id")
	}
}
