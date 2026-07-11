package httpx

import (
	"net/http"
	"testing"
)

func TestNewServerAppliesHTTPTimeouts(t *testing.T) {
	handler := http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
	})

	server := NewServer(":8080", handler)

	if server.Addr != ":8080" {
		t.Fatalf("expected addr :8080, got %q", server.Addr)
	}

	if server.Handler == nil {
		t.Fatalf("expected handler")
	}

	if server.ReadHeaderTimeout != DefaultReadHeaderTimeout {
		t.Fatalf(
			"expected read header timeout %s, got %s",
			DefaultReadHeaderTimeout,
			server.ReadHeaderTimeout,
		)
	}

	if server.ReadTimeout != DefaultReadTimeout {
		t.Fatalf(
			"expected read timeout %s, got %s",
			DefaultReadTimeout,
			server.ReadTimeout,
		)
	}

	if server.WriteTimeout != DefaultWriteTimeout {
		t.Fatalf(
			"expected write timeout %s, got %s",
			DefaultWriteTimeout,
			server.WriteTimeout,
		)
	}

	if server.IdleTimeout != DefaultIdleTimeout {
		t.Fatalf(
			"expected idle timeout %s, got %s",
			DefaultIdleTimeout,
			server.IdleTimeout,
		)
	}

	if server.MaxHeaderBytes != DefaultMaxHeaderBytes {
		t.Fatalf(
			"expected max header bytes %d, got %d",
			DefaultMaxHeaderBytes,
			server.MaxHeaderBytes,
		)
	}
}
