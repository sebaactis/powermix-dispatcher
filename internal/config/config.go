package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type MachineType string

const (
	MachineBatidos MachineType = "batidos"
	MachineSnacks  MachineType = "snacks"
)

const (
	EnvSandbox    = "sandbox"
	EnvProduction = "production"

	StoreMemory   = "memory"
	StorePostgres = "postgres"

	SnacksSandboxURL    = "http://47.110.236.144:5130"
	SnacksProductionURL = "https://ysopenapi.ourvend.com"
)

type Machine struct {
	Type MachineType `json:"type"`
	Name string      `json:"name"`

	DeviceID     string `json:"device_id"`
	DeviceNo     string `json:"device_no"`
	DeviceTypeID string `json:"device_type_id"`

	MID string `json:"mid"`
}

type Kiosk struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Machines []Machine `json:"machines"`
}

func (k Kiosk) Machine(t MachineType) (Machine, bool) {
	for _, m := range k.Machines {
		if m.Type == t {
			return m, true
		}
	}
	return Machine{}, false
}

type Config struct {
	Env string

	Store       string
	DatabaseURL string

	PublicBaseURL string

	BatidosBaseURL        string
	BatidosKey            string
	BatidosSecret         string
	BatidosCallbackSecret string

	SnacksBaseURL        string
	SnacksAccessKey      string
	SnacksAppSecret      string
	SnacksCallbackSecret string

	Kiosks []Kiosk
}

type batidosFile struct {
	BaseURL        string `json:"base_url"`
	Key            string `json:"key"`
	Secret         string `json:"secret"`
	CallbackSecret string `json:"callback_secret"`
}

type snacksFile struct {
	BaseURL        string `json:"base_url"`
	AccessKey      string `json:"access_key"`
	AppSecret      string `json:"app_secret"`
	CallbackSecret string `json:"callback_secret"`
}

type fileConfig struct {
	Env           string      `json:"env"`
	Store         string      `json:"store"`
	DatabaseURL   string      `json:"database_url"`
	PublicBaseURL string      `json:"public_base_url"`
	Batidos       batidosFile `json:"batidos"`
	Snacks        snacksFile  `json:"snacks"`
	Kiosks        []Kiosk     `json:"kiosks"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la configuración %s: %w", path, err)
	}

	var file fileConfig
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("no se pudo interpretar la configuración %s: %w", path, err)
	}

	cfg := &Config{
		Env:                   strings.TrimSpace(file.Env),
		Store:                 strings.TrimSpace(file.Store),
		DatabaseURL:           strings.TrimSpace(file.DatabaseURL),
		PublicBaseURL:         strings.TrimSpace(file.PublicBaseURL),
		BatidosBaseURL:        strings.TrimSuffix(strings.TrimSpace(file.Batidos.BaseURL), "/"),
		BatidosKey:            file.Batidos.Key,
		BatidosSecret:         file.Batidos.Secret,
		BatidosCallbackSecret: file.Batidos.CallbackSecret,
		SnacksBaseURL:         strings.TrimSuffix(strings.TrimSpace(file.Snacks.BaseURL), "/"),
		SnacksAccessKey:       file.Snacks.AccessKey,
		SnacksAppSecret:       file.Snacks.AppSecret,
		SnacksCallbackSecret:  file.Snacks.CallbackSecret,
		Kiosks:                file.Kiosks,
	}

	cfg.applyEnvOverrides()
	cfg.PublicBaseURL = strings.TrimSuffix(cfg.PublicBaseURL, "/")

	if err := cfg.validateEnv(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyEnvOverrides() {
	override(&c.PublicBaseURL, "PUBLIC_BASE_URL")
	override(&c.Store, "STORE")
	override(&c.DatabaseURL, "DATABASE_URL")
	override(&c.BatidosKey, "BATIDOS_KEY")
	override(&c.BatidosSecret, "BATIDOS_SECRET")
	override(&c.SnacksAccessKey, "SNACKS_ACCESS_KEY")
	override(&c.SnacksAppSecret, "SNACKS_APP_SECRET")
	override(&c.BatidosCallbackSecret, "BATIDOS_CALLBACK_SECRET")
	override(&c.SnacksCallbackSecret, "SNACKS_CALLBACK_SECRET")
}

func override(target *string, envName string) {
	if v := strings.TrimSpace(os.Getenv(envName)); v != "" {
		*target = v
	}
}

func (c *Config) applyDefaults() {
	if c.Store == "" {
		c.Store = StoreMemory
	}

	if c.SnacksBaseURL != "" {
		return
	}
	if c.Env == EnvProduction {
		c.SnacksBaseURL = SnacksProductionURL
		return
	}
	c.SnacksBaseURL = SnacksSandboxURL
}
