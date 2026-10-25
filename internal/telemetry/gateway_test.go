package telemetry

import (
	"context"
	"net"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
)

// SetTrustedGateways reaches the Traefik normalizer: behind a trusted gateway
// the observed source is the forwarded client, not the gateway.
func TestSetTrustedGatewaysReadsForwardedClient(t *testing.T) {
	ctx := context.Background()
	ing := New(openDB(t, telemetrydb.Security), nil, quiet())
	obs := &recordingObserver{}
	ing.SetObserver(obs)
	_, gw, err := net.ParseCIDR("192.168.4.111/32")
	if err != nil {
		t.Fatal(err)
	}
	ing.SetTrustedGateways([]*net.IPNet{gw})

	line := `{"source":"traefik","message":"{\"ClientHost\":\"192.168.4.111\",\"DownstreamStatus\":401,` +
		`\"RequestMethod\":\"GET\",\"RequestHost\":\"main.nijmegen.wiebe.xyz\",\"RequestPath\":\"/api/v1/contact\",` +
		`\"request_X-Forwarded-For\":\"198.51.100.1\"}"}`
	if err := ing.Ingest(ctx, KindSecurity, []byte(line)); err != nil {
		t.Fatal(err)
	}
	if len(obs.security) != 1 || obs.security[0].SrcIP != "198.51.100.1" {
		t.Fatalf("observed %+v", obs.security)
	}
}
