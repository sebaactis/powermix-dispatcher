package order

import (
	"context"
	"errors"
	"fmt"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/store"
)

func (e *Engine) Place(ctx context.Context, draft Draft) (store.Order, error) {
	if err := e.validateDraft(draft); err != nil {
		return store.Order{}, err
	}

	if draft.ClientKey != "" {
		existing, err := e.store.OrderByClientKey(ctx, draft.ClientKey)
		switch {
		case err == nil:
			return existing, nil
		case !errors.Is(err, store.ErrNotFound):
			return store.Order{}, err
		}
	}

	if e.gate != nil {
		if err := e.gate.Check(ctx, draft); err != nil {
			return store.Order{}, err
		}
	}

	orderID := e.nextID()
	placed := store.Order{
		ID:          orderID,
		KioskID:     draft.KioskID,
		ComboID:     draft.ComboID,
		ClientKey:   draft.ClientKey,
		AmountCents: draft.AmountCents,
		Currency:    draft.Currency,
		Status:      store.OrderPendingPayment,
		CreatedAt:   e.now(),
		Lines:       make([]store.Line, 0, len(draft.Items)),
	}

	for i, item := range draft.Items {
		lineID := e.nextID()

		placed.Lines = append(placed.Lines, store.Line{
			ID:          lineID,
			OrderID:     orderID,
			Position:    i + 1,
			MachineType: item.Machine.Type,
			Machine:     item.Machine,
			SKU:         item.SKU,
			AmountCents: item.AmountCents,
			State:       dispenser.LinePending,
		})
	}

	if err := e.store.CreateOrder(ctx, placed); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) && draft.ClientKey != "" {
			existing, readErr := e.store.OrderByClientKey(ctx, draft.ClientKey)
			if readErr == nil {
				return existing, nil
			}
		}

		return store.Order{}, err
	}

	return placed, nil
}

func (e *Engine) validateDraft(draft Draft) error {
	if len(draft.Items) == 0 {
		return errors.New("la orden necesita al menos un ítem")
	}

	if draft.AmountCents < 0 {
		return fmt.Errorf("la orden tiene un importe negativo: %d", draft.AmountCents)
	}

	for i, item := range draft.Items {
		position := i + 1

		if item.Machine.Type == "" {
			return fmt.Errorf("ítem %d: falta el tipo de máquina", position)
		}

		if _, ok := e.machines[item.Machine.Type]; !ok {
			return fmt.Errorf("ítem %d: no hay una máquina configurada para el tipo %q", position, item.Machine.Type)
		}

		if item.SKU == "" {
			return fmt.Errorf("ítem %d: falta el SKU", position)
		}

		if !machineIdentified(item.Machine) {
			return fmt.Errorf("ítem %d: falta el identificador de la máquina", position)
		}

		if item.AmountCents < 0 {
			return fmt.Errorf("ítem %d: el importe no puede ser negativo: %d", position, item.AmountCents)
		}
	}

	return nil
}

func machineIdentified(machine dispenser.MachineRef) bool {
	return machine.DeviceID != "" || machine.DeviceNo != "" || machine.MID != ""
}
