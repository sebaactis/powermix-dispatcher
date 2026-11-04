package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

type Item struct {
	MachineType string `json:"machine_type"`
	SKU         string `json:"sku"`
}

type Combo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PriceCents int64  `json:"price_cents"`
	Currency   string `json:"currency"`
	Active     bool   `json:"active"`
	Items      []Item `json:"items"`
}

var ErrNotFound = errors.New("el combo no existe")

type Catalog interface {
	Combo(ctx context.Context, id string) (Combo, error)
	Combos(ctx context.Context) ([]Combo, error)
}

func LoadFile(path string) ([]Combo, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer el catálogo %s: %w", path, err)
	}

	var combos []Combo
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&combos); err != nil {
		return nil, fmt.Errorf("no se pudo interpretar el catálogo %s: %w", path, err)
	}

	if err := validate(combos); err != nil {
		return nil, err
	}

	return combos, nil
}

func validate(combos []Combo) error {
	problems := make([]string, 0)
	seen := make(map[string]struct{}, len(combos))

	for position, combo := range combos {
		label := fmt.Sprintf("%q", combo.ID)
		if combo.ID == "" {
			label = fmt.Sprintf("en la posición %d", position+1)
			problems = append(problems, fmt.Sprintf("el combo %s no tiene id", label))
		} else if _, duplicate := seen[combo.ID]; duplicate {
			problems = append(problems, fmt.Sprintf("el combo %s está declarado dos veces", label))
		} else {
			seen[combo.ID] = struct{}{}
		}

		if combo.Name == "" {
			problems = append(problems, fmt.Sprintf("el combo %s no tiene nombre", label))
		}
		if combo.PriceCents < 0 {
			problems = append(problems, fmt.Sprintf("el combo %s tiene un precio negativo (%d)", label, combo.PriceCents))
		}
		if len(combo.Items) == 0 {
			problems = append(problems, fmt.Sprintf("el combo %s no tiene ítems", label))
		}

		for itemPosition, item := range combo.Items {
			if item.MachineType == "" {
				problems = append(problems, fmt.Sprintf("el combo %s tiene un ítem en la posición %d sin tipo de máquina", label, itemPosition+1))
			}
			if item.SKU == "" {
				problems = append(problems, fmt.Sprintf("el combo %s tiene un ítem en la posición %d sin sku", label, itemPosition+1))
			}
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("catálogo: %s", strings.Join(problems, "; "))
	}

	return nil
}

func cloneCombo(combo Combo) Combo {
	combo.Items = slices.Clone(combo.Items)

	return combo
}
