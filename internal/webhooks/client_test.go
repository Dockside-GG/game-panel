package webhooks

import (
	"net"
	"testing"
)

func TestValidateURL(t *testing.T) {
	t.Parallel()
	if err := ValidateURL("https://discord.com/api/webhooks/123/token", "discord"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateURL("https://events.example.com/dockside", "generic"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		url  string
		kind string
	}{
		{url: "http://events.example.com/hook", kind: "generic"},
		{url: "https://127.0.0.1/hook", kind: "generic"},
		{url: "https://localhost/hook", kind: "generic"},
		{url: "https://example.com/api/webhooks/123/token", kind: "discord"},
	} {
		if err := ValidateURL(test.url, test.kind); err == nil {
			t.Fatalf("ValidateURL(%q, %q) accepted an unsafe URL", test.url, test.kind)
		}
	}
}

func TestPublicIP(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.1.1", "::1"} {
		if publicIP(net.ParseIP(value)) {
			t.Fatalf("publicIP(%q) = true", value)
		}
	}
	for _, value := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !publicIP(net.ParseIP(value)) {
			t.Fatalf("publicIP(%q) = false", value)
		}
	}
}
