package main

import (
	"net/http"
	"testing"
	"time"
)

func TestNewServerSetsTheTimeoutsAndTheHandler(t *testing.T) {
	handler := http.NewServeMux()

	server := newServer("127.0.0.1:9999", handler)

	if server.Addr != "127.0.0.1:9999" {
		t.Fatalf("Addr quedó en %q, se esperaba %q", server.Addr, "127.0.0.1:9999")
	}
	if server.Handler != handler {
		t.Fatalf("Handler no es el mux que se pasó: se esperaba la misma identidad")
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout quedó en %v, se esperaban 5s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 10*time.Second {
		t.Fatalf("ReadTimeout quedó en %v, se esperaban 10s", server.ReadTimeout)
	}
	if server.WriteTimeout != 60*time.Second {
		t.Fatalf("WriteTimeout quedó en %v, se esperaban 60s", server.WriteTimeout)
	}
	if server.IdleTimeout != 60*time.Second {
		t.Fatalf("IdleTimeout quedó en %v, se esperaban 60s", server.IdleTimeout)
	}
	if server.MaxHeaderBytes != 64<<10 {
		t.Fatalf("MaxHeaderBytes quedó en %d, se esperaban %d", server.MaxHeaderBytes, 64<<10)
	}
}
