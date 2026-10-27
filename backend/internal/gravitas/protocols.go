package gravitas

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// registerAccess runs before a successful browser/native login is returned. The
// user and provider origin come from the OAuth grant, never from this JSON.
func (c *Client) registerAccess(ctx context.Context, endpoint, access string) error {
	if len(c.ProtocolRegistry) == 0 || endpoint == "" {
		return nil
	}
	return c.putRegistry(ctx, endpoint, access, c.ProtocolRegistry)
}

// RegisterProtocols reconciles existing devices with the same registry used at
// login. A status proof alone cannot register: IAM also checks the backend key
// and binds both credentials to the same user and application origin.
func (c *Client) RegisterProtocols(ctx context.Context, identity Identity, serviceKey string) error {
	if len(c.ProtocolRegistry) == 0 || identity.Issuer != c.Issuer {
		return nil
	}
	m, err := c.discovery()
	if err != nil {
		return err
	}
	if m.Registry == "" {
		return nil
	}
	body, err := json.Marshal(map[string]any{"session_proof": identity.CheckToken, "registry": c.ProtocolRegistry})
	if err != nil {
		return err
	}
	return c.putRegistry(ctx, c.Issuer+"/protocols/v1/registry", serviceKey, body)
}

func (c *Client) putRegistry(ctx context.Context, endpoint, access string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.HTTP.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != 204 {
		return ErrUnavailable
	}
	return nil
}
