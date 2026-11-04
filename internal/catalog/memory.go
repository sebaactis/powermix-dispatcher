package catalog

import (
	"context"
	"sort"
)

type Memory struct {
	combos map[string]Combo
}

var _ Catalog = (*Memory)(nil)

func NewMemory(combos []Combo) (*Memory, error) {
	if err := validate(combos); err != nil {
		return nil, err
	}

	stored := make(map[string]Combo, len(combos))
	for _, combo := range combos {
		stored[combo.ID] = cloneCombo(combo)
	}

	return &Memory{combos: stored}, nil
}

func (m *Memory) Combo(ctx context.Context, id string) (Combo, error) {
	if id == "" {
		return Combo{}, ErrNotFound
	}

	combo, found := m.combos[id]
	if !found || !combo.Active {
		return Combo{}, ErrNotFound
	}

	return cloneCombo(combo), nil
}

func (m *Memory) Combos(ctx context.Context) ([]Combo, error) {
	active := make([]Combo, 0, len(m.combos))
	for _, combo := range m.combos {
		if combo.Active {
			active = append(active, cloneCombo(combo))
		}
	}

	sort.Slice(active, func(i, j int) bool {
		return active[i].ID < active[j].ID
	})

	return active, nil
}
