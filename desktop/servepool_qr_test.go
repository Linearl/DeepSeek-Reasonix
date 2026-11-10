package main

import (
	"strings"
	"testing"
)

// tl1536 pins: the serve connect QR the settings panel renders must carry a
// phone-reachable host list ordered best-first, and ServePoolStatus must
// keep exposing token + port + lan_hosts — the three fields the frontend QR
// URL is built from. Lose any of them and the QR goes blank or wrong.
func TestLanHostRankOrdersPrivateLanFirst(t *testing.T) {
	cases := []struct {
		host string
		want int
	}{
		{"192.168.1.5", 0},
		{"10.0.0.2", 0},
		{"172.16.3.9", 0},
		{"100.101.2.3", 1}, // Tailscale CGNAT 100.64/10
		{"100.64.0.1", 1},
		{"100.128.0.1", 2}, // outside 100.64/10 → other public
		{"8.8.8.8", 2},
		{"not-an-ip", 3},
	}
	for _, tc := range cases {
		if got := lanHostRank(tc.host); got != tc.want {
			t.Fatalf("lanHostRank(%q) = %d; want %d", tc.host, got, tc.want)
		}
	}
	// Best-first ordering across a mixed candidate set.
	mixed := []string{"8.8.8.8", "100.101.2.3", "192.168.1.5", "10.0.0.2"}
	sortLanHostsForQR(mixed)
	if mixed[0] != "192.168.1.5" || mixed[1] != "10.0.0.2" || mixed[2] != "100.101.2.3" || mixed[3] != "8.8.8.8" {
		t.Fatalf("sorted order = %v; want private LAN first, CGNAT next, public last", mixed)
	}
}

// TestServePoolStatusCarriesQRFields pins the three fields the frontend QR
// depends on: token non-empty (loadOrCreateGatewayToken in the isolated home),
// port present, lan_hosts a []string. The gateway need not be running — the
// panel renders the QR whenever the fields are available.
func TestServePoolStatusCarriesQRFields(t *testing.T) {
	isolateDesktopUserDirs(t)
	a := &App{}
	status := a.ServePoolStatus()
	token, _ := status["token"].(string)
	if strings.TrimSpace(token) == "" {
		t.Fatalf("ServePoolStatus token empty; QR auth would be blank")
	}
	if port, _ := status["port"].(int); port != 18789 {
		t.Fatalf("ServePoolStatus port = %v; want default 18789", status["port"])
	}
	hosts, ok := status["lan_hosts"].([]string)
	if !ok {
		t.Fatalf("ServePoolStatus lan_hosts missing or wrong type: %T", status["lan_hosts"])
	}
	for _, h := range hosts {
		if strings.HasPrefix(h, "127.") {
			t.Fatalf("lan_hosts carries loopback %q; QR must never offer loopback to a phone", h)
		}
	}
}
