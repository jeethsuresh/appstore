package store

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const apnsMaxPayload = 4096

var appleIdentifier = regexp.MustCompile(`^[A-Z0-9]{10}$`)
var appleTopic = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)

type apnsCredentials struct {
	Topic      string `json:"topic"`
	TeamID     string `json:"team_id"`
	KeyID      string `json:"key_id"`
	PrivateKey string `json:"private_key"`
}

type apnsApp struct {
	origin  string
	targets map[string]*apnsClient
}

type apnsClient struct {
	topic, endpoint, header, team string
	key                           *ecdsa.PrivateKey
	http                          *http.Client
	mu                            sync.Mutex
	jwt                           string
	issued                        time.Time
}

func newAPNSClient(environment string, cfg apnsCredentials) (*apnsClient, error) {
	if !appleTopic.MatchString(cfg.Topic) || !appleIdentifier.MatchString(cfg.TeamID) || !appleIdentifier.MatchString(cfg.KeyID) {
		return nil, errors.New("invalid APNs topic, Team ID or Key ID")
	}
	block, rest := pem.Decode([]byte(cfg.PrivateKey))
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("APNs key must be an Apple PKCS#8 PEM key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	key, ok := parsed.(*ecdsa.PrivateKey)
	if err != nil || !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("APNs key must be an ECDSA P-256 key")
	}
	endpoint := "https://api.push.apple.com"
	if environment == "sandbox" {
		endpoint = "https://api.sandbox.push.apple.com"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	protocols := new(http.Protocols)
	protocols.SetHTTP2(true)
	transport.Protocols = protocols
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": cfg.KeyID})
	return &apnsClient{
		topic: cfg.Topic, endpoint: endpoint, header: base64.RawURLEncoding.EncodeToString(header), team: cfg.TeamID, key: key,
		http: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (c *apnsClient) authorization(now time.Time) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.jwt != "" && now.Sub(c.issued) < 45*time.Minute {
		return c.jwt, nil
	}
	claims, _ := json.Marshal(map[string]any{"iss": c.team, "iat": now.Unix()})
	input := c.header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, c.key, digest[:])
	if err != nil {
		return "", err
	}
	var signature [64]byte
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	c.jwt = input + "." + base64.RawURLEncoding.EncodeToString(signature[:])
	c.issued = now
	return c.jwt, nil
}

// The alert avoids exposing event details on the lock screen. The signed-in
// app can consume the original data or refresh when it exceeds Apple's limit.
func apnsPayload(event, subject string, data json.RawMessage) []byte {
	notification := struct {
		APS     map[string]any  `json:"aps"`
		ID      string          `json:"id"`
		Subject string          `json:"subject"`
		Payload json.RawMessage `json:"payload,omitempty"`
		Refresh bool            `json:"refresh,omitempty"`
	}{
		APS: map[string]any{"alert": map[string]string{"body": "New notification"}, "sound": "default"},
		ID:  event, Subject: subject, Payload: data,
	}
	body, err := json.Marshal(notification)
	if err != nil || len(body) > apnsMaxPayload {
		notification.Payload, notification.Refresh = nil, true
		body, _ = json.Marshal(notification)
	}
	return body
}

type apnsResult int

const (
	apnsRetry apnsResult = iota
	apnsAccepted
	apnsInvalidToken
	apnsRejected
)

func (c *apnsClient) send(ctx context.Context, token, event string, body []byte, expires time.Time) apnsResult {
	jwt, err := c.authorization(time.Now())
	if err != nil {
		return apnsRetry
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/3/device/"+token, bytes.NewReader(body))
	if err != nil {
		return apnsRetry
	}
	req.Header.Set("Authorization", "bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apns-topic", c.topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-collapse-id", event)
	req.Header.Set("apns-expiration", strconv.FormatInt(expires.Unix(), 10))
	response, err := c.http.Do(req)
	if err != nil {
		return apnsRetry
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		return apnsAccepted
	}
	var result struct{ Reason string }
	if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result) != nil {
		return apnsRetry
	}
	switch {
	case response.StatusCode == http.StatusGone && (result.Reason == "Unregistered" || result.Reason == "ExpiredToken"):
		return apnsInvalidToken
	case response.StatusCode == http.StatusBadRequest && (result.Reason == "BadDeviceToken" || result.Reason == "DeviceTokenNotForTopic"):
		return apnsInvalidToken
	case response.StatusCode == http.StatusRequestEntityTooLarge:
		return apnsRejected
	default:
		return apnsRetry
	}
}

func validAPNSToken(token string) bool {
	if token == "" || len(token) > 1024 || len(token)%2 != 0 {
		return false
	}
	return strings.Trim(token, "0123456789abcdefABCDEF") == ""
}
