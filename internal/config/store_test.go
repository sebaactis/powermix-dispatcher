package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsTheStoreToMemory(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Store != StoreMemory {
		t.Fatalf("store = %q, se esperaba %q cuando el archivo no lo declara", cfg.Store, StoreMemory)
	}
	if cfg.DatabaseURL != "" {
		t.Fatalf("database url = %q, se esperaba vacía", cfg.DatabaseURL)
	}
}

func TestLoadReadsThePostgresStoreFromTheFile(t *testing.T) {
	body := strings.Replace(validConfig, `"env": "sandbox",`,
		`"env": "sandbox", "store": "postgres", "database_url": "postgres://usuario:clave@localhost:5432/compras",`, 1)

	cfg, err := Load(writeTempConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Store != StorePostgres {
		t.Fatalf("store = %q, se esperaba %q", cfg.Store, StorePostgres)
	}
	if cfg.DatabaseURL != "postgres://usuario:clave@localhost:5432/compras" {
		t.Fatalf("database url = %q, se esperaba la del archivo", cfg.DatabaseURL)
	}
}

func TestLoadReadsTheStoreAndTheDatabaseURLFromTheEnvironment(t *testing.T) {
	t.Setenv("STORE", "postgres")
	t.Setenv("DATABASE_URL", "postgres://otro:otro@db:5432/compras")

	cfg, err := Load(writeTempConfig(t, validConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Store != StorePostgres {
		t.Fatalf("store = %q, se esperaba el del entorno", cfg.Store)
	}
	if cfg.DatabaseURL != "postgres://otro:otro@db:5432/compras" {
		t.Fatalf("database url = %q, se esperaba la del entorno", cfg.DatabaseURL)
	}
}

func TestLoadRejectsAnUnknownStore(t *testing.T) {
	body := strings.Replace(validConfig, `"env": "sandbox",`, `"env": "sandbox", "store": "sqlite",`, 1)

	_, err := Load(writeTempConfig(t, body))
	if err == nil {
		t.Fatal("Load aceptó un store desconocido")
	}
	if !strings.Contains(err.Error(), "sqlite") {
		t.Fatalf("error = %q, se esperaba que nombre el store rechazado", err)
	}
}

func TestLoadRejectsPostgresWithoutDatabaseURL(t *testing.T) {
	body := strings.Replace(validConfig, `"env": "sandbox",`, `"env": "sandbox", "store": "postgres",`, 1)

	_, err := Load(writeTempConfig(t, body))
	if err == nil {
		t.Fatal("Load aceptó postgres sin database_url")
	}
	if !strings.Contains(err.Error(), "database_url") {
		t.Fatalf("error = %q, se esperaba que nombre database_url", err)
	}
}

func TestLoadAcceptsMemoryWithoutDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	cfg, err := Load(writeTempConfig(t, validConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Store != StoreMemory {
		t.Fatalf("store = %q, se esperaba %q", cfg.Store, StoreMemory)
	}
}

func TestDockerConfigPointsAtTheComposeServices(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://compras:compras@db:5432/compras?sslmode=disable")

	cfg, err := Load(filepath.Join("..", "..", "config.docker.json"))
	if err != nil {
		t.Fatalf("config.docker.json no es válida: %v", err)
	}

	if cfg.Store != StorePostgres {
		t.Fatalf("store = %q, se esperaba %q", cfg.Store, StorePostgres)
	}
	if cfg.BatidosBaseURL != "http://fakeapi:9401" {
		t.Fatalf("batidos base url = %q, se esperaba el servicio fakeapi", cfg.BatidosBaseURL)
	}
	if cfg.SnacksBaseURL != "http://fakeapi:9402" {
		t.Fatalf("snacks base url = %q, se esperaba el servicio fakeapi", cfg.SnacksBaseURL)
	}
	if cfg.PublicBaseURL != "http://127.0.0.1:18080" {
		t.Fatalf("public base url = %q, se esperaba el puerto publicado", cfg.PublicBaseURL)
	}
	if len(cfg.Kiosks) != 1 || cfg.Kiosks[0].Machines[0].DeviceID != "dev-e2e-1" {
		t.Fatalf("los muebles de config.docker.json no son los que siembra el fake: %+v", cfg.Kiosks)
	}
}
