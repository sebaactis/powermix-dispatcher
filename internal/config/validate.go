package config

import (
	"fmt"
	"net/url"
	"strings"
)

func (c *Config) validateEnv() error {
	switch c.Env {
	case EnvSandbox, EnvProduction:
		return nil
	default:
		return fmt.Errorf("configuración: el entorno %q no es %q ni %q", c.Env, EnvSandbox, EnvProduction)
	}
}

func (c *Config) validate() error {
	var problems []string

	switch c.Store {
	case StoreMemory:
	case StorePostgres:
		if c.DatabaseURL == "" {
			problems = append(problems, fmt.Sprintf("store es %q y falta database_url (archivo o DATABASE_URL)", StorePostgres))
		}
	default:
		problems = append(problems, fmt.Sprintf("store %q no es %q ni %q", c.Store, StoreMemory, StorePostgres))
	}

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
