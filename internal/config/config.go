package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
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

func (c *Config) validateEnv() error {
	switch c.Env {
	case EnvSandbox, EnvProduction:
		return nil
	default:
		return fmt.Errorf("configuración: el entorno %q no es %q ni %q", c.Env, EnvSandbox, EnvProduction)
	}
}

func (c *Config) applyDefaults() {
	if c.SnacksBaseURL != "" {
		return
	}
	if c.Env == EnvProduction {
		c.SnacksBaseURL = SnacksProductionURL
		return
	}
	c.SnacksBaseURL = SnacksSandboxURL
}

func (c *Config) validate() error {
	var problems []string

	if c.BatidosBaseURL == "" {
		problems = append(problems, "batidos.base_url es obligatorio (el proveedor no documentó el dominio de la API)")
	}
	if c.BatidosKey == "" {
		problems = append(problems, "la key de batidos es obligatoria (archivo o BATIDOS_KEY)")
	}
	if c.BatidosSecret == "" {
		problems = append(problems, "el secret de batidos es obligatorio (archivo o BATIDOS_SECRET)")
	}
	if c.SnacksBaseURL == "" {
		problems = append(problems, "no se pudo resolver snacks.base_url")
	}
	if c.SnacksAccessKey == "" {
		problems = append(problems, "el access key de snacks es obligatorio (archivo o SNACKS_ACCESS_KEY)")
	}
	if c.SnacksAppSecret == "" {
		problems = append(problems, "el app secret de snacks es obligatorio (archivo o SNACKS_APP_SECRET)")
	}
	if len(c.Kiosks) == 0 {
		problems = append(problems, "se requiere al menos un mueble")
	}

	if c.PublicBaseURL != "" {
		problems = append(problems, publicBaseURLProblems(c.PublicBaseURL, c.Env)...)
	}

	if c.BatidosCallbackSecret != "" {
		problems = append(problems, callbackSecretProblems("batidos.callback_secret", c.BatidosCallbackSecret)...)
	}

	if c.SnacksCallbackSecret != "" {
		problems = append(problems, callbackSecretProblems("snacks.callback_secret", c.SnacksCallbackSecret)...)
	}

	seen := make(map[string]struct{}, len(c.Kiosks))
	for _, kiosk := range c.Kiosks {
		if kiosk.ID == "" {
			problems = append(problems, "un mueble no tiene id")
			continue
		}
		if _, dup := seen[kiosk.ID]; dup {
			problems = append(problems, fmt.Sprintf("el mueble %s está declarado dos veces", kiosk.ID))
			continue
		}
		seen[kiosk.ID] = struct{}{}
		problems = append(problems, kioskProblems(kiosk)...)
	}

	if len(problems) > 0 {
		return fmt.Errorf("configuración: %s", strings.Join(problems, "; "))
	}
	return nil
}

func kioskProblems(kiosk Kiosk) []string {
	var problems []string

	for _, required := range []MachineType{MachineBatidos, MachineSnacks} {
		count := 0
		for _, m := range kiosk.Machines {
			if m.Type == required {
				count++
			}
		}
		switch count {
		case 1:
		case 0:
			problems = append(problems, fmt.Sprintf("el mueble %s no tiene máquina de %s", kiosk.ID, required))
		default:
			problems = append(problems, fmt.Sprintf("el mueble %s tiene %d máquinas de %s, se esperaba exactamente una", kiosk.ID, count, required))
		}
	}

	for _, m := range kiosk.Machines {
		switch m.Type {
		case MachineBatidos:
			if m.DeviceID == "" || m.DeviceNo == "" || m.DeviceTypeID == "" {
				problems = append(problems, fmt.Sprintf("mueble %s: la máquina de batidos necesita device_id, device_no y device_type_id", kiosk.ID))
			}
		case MachineSnacks:
			if m.MID == "" {
				problems = append(problems, fmt.Sprintf("mueble %s: la máquina de snacks necesita mid", kiosk.ID))
			}
		default:
			problems = append(problems, fmt.Sprintf("mueble %s: tipo de máquina desconocido %q", kiosk.ID, m.Type))
		}
	}

	return problems
}

func publicBaseURLProblems(baseURL, env string) []string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return []string{fmt.Sprintf("public_base_url %q no es una URL absoluta con host", baseURL)}
	}

	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return []string{fmt.Sprintf("public_base_url %q debe ser http o https", baseURL)}
	}

	if env == EnvProduction && parsed.Scheme != "https" {
		return []string{"public_base_url debe ser https en producción: el notifyUrl lleva la ruta secreta del callback de batidos"}
	}

	return nil
}

func callbackSecretProblems(field, secret string) []string {
	if secret == "." || secret == ".." {
		return []string{fmt.Sprintf("%s no puede ser %q: el path del callback se colapsa y nunca matchea", field, secret)}
	}

	for _, char := range secret {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}

		if char == '.' || char == '-' || char == '_' {
			continue
		}

		return []string{fmt.Sprintf("%s tiene el carácter %q y sólo admite letras, números, punto, guion y guion bajo", field, char)}
	}

	return nil
}
