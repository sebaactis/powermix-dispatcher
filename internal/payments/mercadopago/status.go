package mercadopago

import "compras-remotas/internal/payments"

func mapStatus(status string) payments.ChargeStatus {
	switch status {
	case "created":
		return payments.ChargePending
	case "processed":
		return payments.ChargeApproved
	case "expired":
		return payments.ChargeExpired
	case "canceled":
		return payments.ChargeCancelled
	default:
		return payments.ChargeUnknown
	}
}
