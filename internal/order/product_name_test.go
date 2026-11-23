package order_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"compras-remotas/internal/dispenser"
	"compras-remotas/internal/machine"
	"compras-remotas/internal/notify"
	"compras-remotas/internal/order"
	"compras-remotas/internal/store"
)

func TestEnginePartialNoticeShowsTheProductNames(t *testing.T) {
	notifier := &recordingNotifier{}
	orders := store.NewMemory()
	engine := order.New(orders, map[machine.Kind]dispenser.Dispenser{
		"batidos": &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "code-batidos"}},
		"snacks":  &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "sub-snacks"}},
	}, order.WithClock(frozenClock()), order.WithIDGenerator(sequentialIDs()), order.WithNotifier(notifier))

	draft := comboDraft()
	draft.Items[0].Name = "Batido de vainilla"
	draft.Items[1].Name = "Alfajor"

	placed := place(t, engine, draft)
	if err := engine.MarkPaid(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}

	stored := mustOrder(t, orders, placed.ID)
	names := map[string]string{}
	for _, line := range stored.Lines {
		names[string(line.Machine.Kind)] = line.Name
	}
	if names["batidos"] != "Batido de vainilla" || names["snacks"] != "Alfajor" {
		t.Fatalf("las líneas no guardaron el nombre del producto: %+v", names)
	}

	fx := &notifyFixture{engine: engine, orders: orders, orderID: placed.ID}
	if err := fx.deliverLine(t, "batidos", "cb-1", "code-batidos"); err != nil {
		t.Fatalf("callback entregado: %v", err)
	}
	if err := fx.failLine(t, "snacks", "cb-2", "sub-snacks"); err != nil {
		t.Fatalf("callback fallido: %v", err)
	}

	event := notifier.only(t)
	if event.Kind != notify.KindOrderPartial {
		t.Fatalf("Kind=%q, se esperaba %q", event.Kind, notify.KindOrderPartial)
	}
	for _, want := range []string{"Batido de vainilla", "Alfajor"} {
		if !strings.Contains(event.Detail, want) {
			t.Fatalf("el detalle %q no contiene %q", event.Detail, want)
		}
	}
	for _, unwanted := range []string{"batidos/", "snacks/"} {
		if strings.Contains(event.Detail, unwanted) {
			t.Fatalf("el detalle %q todavía muestra el SKU (%q)", event.Detail, unwanted)
		}
	}
}

func TestEngineInDoubtNoticeNamesTheProducts(t *testing.T) {
	notifier := &recordingNotifier{}
	orders := store.NewMemory()
	engine := order.New(orders, map[machine.Kind]dispenser.Dispenser{
		"batidos": &stubMachine{attempt: dispenser.DispatchResult{Accepted: true, ProviderRef: "code-batidos"}},
		"snacks":  &stubMachine{err: errors.New("snacks: timeout")},
	}, order.WithClock(frozenClock()), order.WithIDGenerator(sequentialIDs()), order.WithNotifier(notifier))

	draft := comboDraft()
	draft.Items[0].Name = "Batido de vainilla"
	draft.Items[1].Name = "Alfajor"

	placed := place(t, engine, draft)
	if err := engine.MarkPaid(context.Background(), placed.ID, engineNow); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}

	fx := &notifyFixture{engine: engine, orders: orders, orderID: placed.ID}
	if err := fx.deliverLine(t, "batidos", "cb-1", "code-batidos"); err != nil {
		t.Fatalf("callback entregado: %v", err)
	}

	if got := mustOrder(t, orders, placed.ID).Status; got != store.OrderInDoubt {
		t.Fatalf("Status=%q, se esperaba %q", got, store.OrderInDoubt)
	}

	event := notifier.only(t)
	if event.Kind != notify.KindOrderInDoubt {
		t.Fatalf("Kind=%q, se esperaba %q", event.Kind, notify.KindOrderInDoubt)
	}
	for _, want := range []string{"se entregó Batido de vainilla", "quedó en duda Alfajor", "hace falta una persona"} {
		if !strings.Contains(event.Detail, want) {
			t.Fatalf("el detalle %q no contiene %q", event.Detail, want)
		}
	}
}
