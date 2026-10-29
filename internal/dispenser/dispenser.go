// Package dispenser define el puerto por el que el motor de órdenes maneja una
// máquina expendedora.
//
// El motor nunca debe enterarse de que una bebida requiere un código de canje
// mientras un snack se despacha de forma directa: desde acá los dos se ven
// igual. Cada proveedor implementa esta interfaz en su propio subpaquete
// (batidos, snacks).
package dispenser

import "context"

// LineState es el ciclo de vida de un producto dentro de una orden. Las dos
// líneas de un combo evolucionan de forma independiente.
type LineState string

const (
	LinePending   LineState = "pending"
	LineSent      LineState = "sent"
	LineBrewing   LineState = "brewing"
	LineDelivered LineState = "delivered"
	LineFailed    LineState = "failed"
	// LineInDoubt significa que no sabemos si el producto salió. Nunca se
	// reintenta a ciegas: primero se reconcilia (ver Reconcile).
	LineInDoubt LineState = "in_doubt"
)

// MachineRef identifica una máquina física y las direcciones con las que
// responde en cada proveedor.
type MachineRef struct {
	KioskID      string
	Type         string // "batidos" o "snacks"
	Name         string
	DeviceID     string // Batidos
	DeviceNo     string // Batidos
	DeviceTypeID string // Batidos
	MID          string // Snacks
}

// Availability es la respuesta del chequeo previo: es lo que habilita o no el QR.
type Availability struct {
	Available bool
	Reason    string
}

// Request es un producto a despachar, correspondiente a una línea de la orden.
type Request struct {
	OrderID     string
	LineID      string
	Machine     MachineRef
	SKU         string // goodsId de Batidos o productId de Snacks
	AmountCents int64
}

// Attempt es la respuesta inmediata a Dispatch. "Aceptado" nunca significa
// "entregado".
type Attempt struct {
	Accepted bool
	Ref      string // orderNo de Batidos o subOrderId de Snacks
	Detail   string
}

// Status es la verdad reconciliada sobre una línea.
type Status struct {
	State  LineState
	Ref    string
	Reason string
}

type Dispenser interface {
	// Check informa si la máquina puede vender en este momento, antes de cobrar.
	Check(ctx context.Context, machine MachineRef) (Availability, error)
	// Dispatch le pide a la máquina que prepare o entregue el producto.
	Dispatch(ctx context.Context, req Request) (Attempt, error)
	// Reconcile le pregunta al proveedor qué pasó realmente con un intento
	// ambiguo. Nunca despacha: solo lee.
	Reconcile(ctx context.Context, req Request) (Status, error)
}
