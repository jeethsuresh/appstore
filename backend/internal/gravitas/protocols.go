package gravitas

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
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

// RegisterNotificationSession establishes discovery for an app's own login,
// and seals its status-only credential for revocation checks before delivery.
func (c *Client) RegisterNotificationSession(ctx context.Context, origin, proof, serviceKey string) (Identity, string, error) {
	if !proofPattern.MatchString(proof) {
		return Identity{}, "", ErrLoginRequired
	}
	m, err := c.discovery()
	if err != nil {
		return Identity{}, "", ErrUnavailable
	}
	status, err := c.readFamilyStatus(ctx, http.MethodGet, m.SessionCheck, proof, nil)
	if err != nil {
		return Identity{}, "", err
	}
	if status.Origin != origin {
		return Identity{}, "", ErrLoginRequired
	}
	var registry struct {
		Protocols []json.RawMessage `json:"protocols"`
	}
	if json.Unmarshal(c.ProtocolRegistry, &registry) != nil || len(registry.Protocols) != 1 {
		return Identity{}, "", ErrUnavailable
	}
	body, err := json.Marshal(map[string]any{"session_proof": proof, "specification": registry.Protocols[0]})
	if err != nil {
		return Identity{}, "", ErrUnavailable
	}
	registered, err := c.readFamilyStatus(ctx, http.MethodPut, c.Issuer+"/protocols/v1/registry/appstore", serviceKey, bytes.NewReader(body))
	if err != nil {
		return Identity{}, "", err
	}
	if registered != status {
		return Identity{}, "", ErrUnavailable
	}
	identity := Identity{Issuer: status.Issuer, Subject: status.Subject, SessionID: status.SessionID, CheckToken: proof, SessionExpires: status.Expires}
	sealed, err := c.seal("notification-session", identity)
	return identity, sealed, err
}

func (c *Client) CheckNotificationSession(ctx context.Context, origin, subject, sealed string) error {
	var identity Identity
	if c.open("notification-session", sealed, &identity) != nil || identity.Issuer != c.Issuer ||
		identity.Subject != subject || identity.SessionExpires <= time.Now().Unix() {
		return ErrLoginRequired
	}
	m, err := c.discovery()
	if err != nil {
		return ErrUnavailable
	}
	status, err := c.readFamilyStatus(ctx, http.MethodGet, m.SessionCheck, identity.CheckToken, nil)
	if err != nil {
		return err
	}
	if status.Subject != identity.Subject || status.Origin != origin ||
		status.SessionID != identity.SessionID || status.Expires != identity.SessionExpires {
		return ErrLoginRequired
	}
	return nil
}
