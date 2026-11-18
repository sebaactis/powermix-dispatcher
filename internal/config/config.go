package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"compras-remotas/internal/machine"
)

const (
	EnvSandbox    = "sandbox"
	EnvProduction = "production"

	StoreMemory   = "memory"
	StorePostgres = "postgres"

	SnacksSandboxURL    = "http://47.110.236.144:5130"
	SnacksProductionURL = "https://ysopenapi.ourvend.com"

	MercadoPagoURL = "https://api.mercadopago.com"

	TelegramURL = "https://api.telegram.org"
)

type Machine struct {
	Type machine.Kind `json:"type"`
	Name string       `json:"name"`

	DeviceID     string `json:"device_id"`
	DeviceNo     string `json:"device_no"`
	DeviceTypeID string `json:"device_type_id"`

	MID string `json:"mid"`
}

type Kiosk struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	MPPosID  string    `json:"mp_pos_id"`
	Machines []Machine `json:"machines"`
}

func (k Kiosk) Machine(t machine.Kind) (Machine, bool) {
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

	MercadoPagoBaseURL        string
	MercadoPagoAccessToken    string
	MercadoPagoWebhookSecret  string
	MercadoPagoCallbackSecret string

	TelegramBaseURL  string
	TelegramBotToken string
	TelegramChatID   string

	Kiosks []Kiosk
}

func (c *Config) TelegramEnabled() bool {
	return c.TelegramBotToken != ""
}

func (c *Config) MercadoPagoEnabled() bool {
	return c.MercadoPagoAccessToken != ""
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

type mercadoPagoFile struct {
	BaseURL        string `json:"base_url"`
	AccessToken    string `json:"access_token"`
	WebhookSecret  string `json:"webhook_secret"`
	CallbackSecret string `json:"callback_secret"`
}

type telegramFile struct {
	BaseURL  string `json:"base_url"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type fileConfig struct {
	Env           string          `json:"env"`
	Store         string          `json:"store"`
	DatabaseURL   string          `json:"database_url"`
	PublicBaseURL string          `json:"public_base_url"`
	Batidos       batidosFile     `json:"batidos"`
	Snacks        snacksFile      `json:"snacks"`
	MercadoPago   mercadoPagoFile `json:"mercadopago"`
	Telegram      telegramFile    `json:"telegram"`
	Kiosks        []Kiosk         `json:"kiosks"`
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

		MercadoPagoBaseURL:        strings.TrimSpace(file.MercadoPago.BaseURL),
		MercadoPagoAccessToken:    file.MercadoPago.AccessToken,
		MercadoPagoWebhookSecret:  file.MercadoPago.WebhookSecret,
		MercadoPagoCallbackSecret: file.MercadoPago.CallbackSecret,

		TelegramBaseURL:  strings.TrimSpace(file.Telegram.BaseURL),
		TelegramBotToken: strings.TrimSpace(file.Telegram.BotToken),
		TelegramChatID:   strings.TrimSpace(file.Telegram.ChatID),

		Kiosks: file.Kiosks,
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
	override(&c.MercadoPagoBaseURL, "MP_BASE_URL")
	override(&c.MercadoPagoAccessToken, "MP_ACCESS_TOKEN")
	override(&c.MercadoPagoWebhookSecret, "MP_WEBHOOK_SECRET")
	override(&c.MercadoPagoCallbackSecret, "MP_CALLBACK_SECRET")
	override(&c.TelegramBaseURL, "TELEGRAM_BASE_URL")
	override(&c.TelegramBotToken, "TELEGRAM_BOT_TOKEN")
	override(&c.TelegramChatID, "TELEGRAM_CHAT_ID")
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

	c.MercadoPagoBaseURL = strings.TrimSuffix(c.MercadoPagoBaseURL, "/")
	if c.MercadoPagoBaseURL == "" {
		c.MercadoPagoBaseURL = MercadoPagoURL
	}

	c.TelegramBaseURL = strings.TrimSuffix(c.TelegramBaseURL, "/")
	if c.TelegramBaseURL == "" {
		c.TelegramBaseURL = TelegramURL
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
