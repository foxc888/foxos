package mosdns

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestClientStatusUsesRealTCPCheck(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := NewClient("http://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status := client.Status(ctx)
	if !status.Configured || !status.Online || status.Transport != "tcp" {
		t.Fatalf("status=%+v", status)
	}
}

func TestNewClientRejectsPublicEndpointCredentials(t *testing.T) {
	t.Parallel()
	tests := []string{"", "udp://10.0.0.3:53", "http://user:pass@10.0.0.3:9090", "http://localhost:9090"}
	for _, endpoint := range tests {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			if _, err := NewClient(endpoint); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
