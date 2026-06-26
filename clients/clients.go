// Package clients holds the outbound HTTP clients the preview service uses. The
// only collaborator is the document source: the service reads a document's
// metadata (to decide whether it can be previewed) and its content bytes (to
// render). The source owner-filters on the user subject, so every call goes out ON
// BEHALF OF the user via token exchange — never with the service's own identity,
// which would let it read documents the user does not own. A call without a
// subject token fails closed.
package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gmb-lib/go-authbyte/authclient"
)

// Doer issues a background DPoP request on behalf of an end user via token
// exchange. *authclient.Client satisfies it; tests inject a stub.
type Doer interface {
	DoServiceOnBehalf(ctx context.Context, audience, scope, subjectSub, subjectToken, method, fullURL string, reqHeader http.Header, body []byte) (*authclient.BackgroundResponse, error)
}

// OnBehalf carries the end-user identity a source call acts for: the user's
// subject (the delegated-token cache key) and the raw inbound token to exchange.
// A call without a subject token cannot reach a user-owned document — the client
// fails closed rather than falling back to the service's own identity.
type OnBehalf struct {
	Sub   string
	Token string
}

// HTTPError is returned when a collaborator responds with a non-2xx status; it
// carries the status so callers can map it onto their own response (in particular
// the source's not-found, which is also how it reports a document the user does
// not own).
type HTTPError struct {
	Service    string
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: upstream status %d: %s", e.Service, e.StatusCode, e.Body)
}

// doJSONOnBehalf issues a request acting on behalf of the end user (token
// exchange) so the callee owner-filters on the user, and decodes the JSON response
// into out when non-nil. It fails closed when no subject token is present.
func doJSONOnBehalf(ctx context.Context, d Doer, service, audience, scope, method, url string, obo OnBehalf, out any) error {
	resp, err := doOnBehalf(ctx, d, service, audience, scope, method, url, obo)
	if err != nil {
		return err
	}
	if out != nil && len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, out); err != nil {
			return fmt.Errorf("%s: decode response: %w", service, err)
		}
	}

	return nil
}

// doBytesOnBehalf issues a request acting on behalf of the end user and returns
// the raw response body (the document content). It fails closed when no subject
// token is present.
func doBytesOnBehalf(ctx context.Context, d Doer, service, audience, scope, method, url string, obo OnBehalf) ([]byte, error) {
	resp, err := doOnBehalf(ctx, d, service, audience, scope, method, url, obo)
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

// doOnBehalf is the shared on-behalf request: it fails closed without a subject
// token, exchanges the token for the target audience/scope, and maps a non-2xx
// status onto an HTTPError the caller can translate.
func doOnBehalf(ctx context.Context, d Doer, service, audience, scope, method, url string, obo OnBehalf) (*authclient.BackgroundResponse, error) {
	if obo.Token == "" {
		return nil, fmt.Errorf("%s: missing on-behalf-of subject token", service)
	}

	resp, err := d.DoServiceOnBehalf(ctx, audience, scope, obo.Sub, obo.Token, method, url, http.Header{}, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", service, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Service: service, StatusCode: resp.StatusCode, Body: string(resp.Body)}
	}

	return resp, nil
}
