package gravitas

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRegistryRequestUsesGrantCredentialAndReturnsFailure(t *testing.T) {
	c, _ := fixture(t)
	c.ProtocolRegistry = json.RawMessage(`{"schema_version":1,"protocols":[]}`)
	calls := 0
	c.HTTP.Transport = federationTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "PUT" || r.URL.String() != c.Issuer+"/app/v1/protocols/registry" || r.Header.Get("Authorization") != "Bearer grant-access" {
			t.Fatal("invalid registry request", r.URL)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil || string(data) != string(c.ProtocolRegistry) {
			t.Fatal(string(data), err)
		}
		status := 204
		if calls == 2 {
			status = 503
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err := c.registerAccess(t.Context(), c.Issuer+"/app/v1/protocols/registry", "grant-access"); err != nil {
		t.Fatal(err)
	}
	if err := c.registerAccess(t.Context(), c.Issuer+"/app/v1/protocols/registry", "grant-access"); err != ErrUnavailable {
		t.Fatal("failed registration accepted", err)
	}
}

func TestLoginPublishesPCRBeforeReturningIdentity(t *testing.T) {
	for _, status := range []int{204, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c, _ := fixture(t)
			c.ProtocolRegistry = json.RawMessage(`{"schema_version":1,"protocols":[]}`)
			base := c.HTTP.Transport
			registered := false
			c.HTTP.Transport = federationTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/app/v1/protocols/registry" {
					registered = true
					if r.Header.Get("Authorization") != "Bearer private-opaque-token" {
						t.Fatal("wrong grant credential")
					}
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
				}
				response, err := base.RoundTrip(r)
				if err == nil && r.URL.Path == "/.well-known/iam-configuration" {
					var m map[string]any
					json.NewDecoder(response.Body).Decode(&m)
					response.Body.Close()
					m["protocol_registry_endpoint"] = c.Issuer + "/app/v1/protocols/registry"
					data, _ := json.Marshal(m)
					response.Body = io.NopCloser(strings.NewReader(string(data)))
				}
				return response, err
			})
			identity, err := c.exchange("one-use-code", strings.Repeat("v", 43), c.AppURL+"/auth/gravitas/callback")
			if !registered {
				t.Fatal("login did not publish its PCR")
			}
			if status == 204 && (err != nil || identity.Subject == "") {
				t.Fatal(identity, err)
			}
			if status == 503 && err != ErrUnavailable {
				t.Fatal("failed registration returned a successful login", err)
			}
		})
	}
}
