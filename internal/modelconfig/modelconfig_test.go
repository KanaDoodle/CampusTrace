package modelconfig

import (
	"net"
	"net/http"
	"testing"
)

func TestValidate(t *testing.T) {
	valid := Config{URL: "https://api.example.com/v1/chat/completions", Model: "model-1", APIKey: "test-key"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid model config: %v", err)
	}
	for _, raw := range []string{
		"http://api.example.com/v1/chat/completions",
		"https://localhost/v1/chat/completions",
		"https://10.0.0.1/v1/chat/completions",
		"https://api.example.com:8443/v1/chat/completions",
		"https://user:pass@api.example.com/v1/chat/completions",
		"https://api.example.com/v1/chat/completions?key=secret",
		"https://api.example.com/v1/chat/completions#part",
		"https://api.example.com",
	} {
		v := valid
		v.URL = raw
		if err := v.Validate(); err == nil {
			t.Errorf("accepted invalid URL %q", raw)
		}
	}
	for _, v := range []Config{{URL: valid.URL, Model: valid.Model}, {URL: valid.URL, APIKey: valid.APIKey}, {URL: valid.URL, Model: "bad\nname", APIKey: valid.APIKey}} {
		if err := v.Validate(); err == nil {
			t.Errorf("accepted incomplete or unsafe config")
		}
	}
}

func TestPublicIP(t *testing.T) {
	for address, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "10.0.0.1": false, "172.16.1.1": false,
		"192.168.1.1": false, "169.254.1.1": false, "100.64.0.1": false,
		"198.18.0.1": false, "192.0.2.1": false, "::1": false,
		"fc00::1": false, "fe80::1": false, "2001:db8::1": false,
	} {
		if got := PublicIP(net.ParseIP(address)); got != want {
			t.Errorf("PublicIP(%s) = %v, want %v", address, got, want)
		}
	}
}

func TestPublicClientRejectsRedirect(t *testing.T) {
	client := PublicClient()
	if client.CheckRedirect(&http.Request{}, nil) == nil {
		t.Fatal("custom provider redirects must be rejected")
	}
}
