package batidos

import (
	"testing"
	"time"
)

// Vectores calculados con `openssl md5`, independientes de esta implementación.
func TestKeyMD5MatchesReference(t *testing.T) {
	cases := []struct {
		name        string
		key, secret string
		timestamp   string
		want        string
	}{
		{"first", "mykey", "mysecret", "1719999999999", "ec3ef2ff342b30cb1d3dca7b5eff6a72"},
		{"second", "mykey", "mysecret", "1751234567890", "7ea2808fe46d5ba2f42e98e2f78de468"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := KeyMD5(tc.key, tc.secret, tc.timestamp); got != tc.want {
				t.Fatalf("KeyMD5 = %q, se esperaba %q", got, tc.want)
			}
		})
	}
}

// La concatenación cruda key+secret+timestamp no debe confundir componentes
// distintos.
func TestKeyMD5IsSensitiveToEveryPart(t *testing.T) {
	base := KeyMD5("mykey", "mysecret", "1719999999999")

	variants := map[string]string{
		"key":       KeyMD5("mykez", "mysecret", "1719999999999"),
		"secret":    KeyMD5("mykey", "mysecrez", "1719999999999"),
		"timestamp": KeyMD5("mykey", "mysecret", "1719999999998"),
	}

	for part, got := range variants {
		if got == base {
			t.Fatalf("cambiar %s produjo la misma firma", part)
		}
	}
}

// La campaña de canje viaja en hora de China: un instante UTC se corre +8 horas.
func TestCampaignTimeUsesChinaClock(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"mismo día", time.Date(2026, 3, 25, 8, 30, 42, 0, time.UTC), "2026-03-25 16:30:42"},
		{"cruza la medianoche", time.Date(2026, 12, 31, 20, 0, 0, 0, time.UTC), "2027-01-01 04:00:00"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CampaignTime(tc.in); got != tc.want {
				t.Fatalf("CampaignTime = %q, se esperaba %q", got, tc.want)
			}
		})
	}
}

func TestParseCampaignTimeRoundTrip(t *testing.T) {
	const stamp = "2026-03-25 16:30:42"

	parsed, err := ParseCampaignTime(stamp)
	if err != nil {
		t.Fatalf("ParseCampaignTime: %v", err)
	}
	if parsed.UTC() != time.Date(2026, 3, 25, 8, 30, 42, 0, time.UTC) {
		t.Fatalf("parseado = %s UTC, se esperaba 2026-03-25 08:30:42 UTC", parsed.UTC())
	}
	if back := CampaignTime(parsed); back != stamp {
		t.Fatalf("ida y vuelta = %q, se esperaba %q", back, stamp)
	}
}

func TestSignatureHeadersCarryTheThreeRequiredFields(t *testing.T) {
	now := time.Date(2026, 3, 25, 8, 30, 42, 0, time.UTC)
	headers := SignatureHeaders("mykey", "mysecret", now)

	want := map[string]string{
		"key":       "mykey",
		"timestamp": "1774427442000",
	}
	for name, value := range want {
		if headers[name] != value {
			t.Fatalf("header %q = %q, se esperaba %q", name, headers[name], value)
		}
	}
	if headers["key-md5"] != KeyMD5("mykey", "mysecret", headers["timestamp"]) {
		t.Fatalf("el key-md5 %q no corresponde al timestamp enviado", headers["key-md5"])
	}
}
