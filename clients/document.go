package clients

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
)

// Documents is the client for the document source — the platform's owner of
// document bytes and canonical hashes. The preview service reads metadata (to
// decide renderability and to key a render) and, only when a render is warranted,
// the content bytes themselves. Both reads go out on behalf of the user.
type Documents struct {
	doer     Doer
	baseURL  string
	audience string
}

// NewDocuments builds a document-source client over the given outbound doer.
func NewDocuments(d Doer, baseURL, audience string) *Documents {
	return &Documents{doer: d, baseURL: strings.TrimRight(baseURL, "/"), audience: audience}
}

const scopeDocRead = "documents:read"

// Meta is the document-metadata projection the preview service needs: the media
// type (to decide whether the document can be previewed), the content hash (to key
// a render), and the size (to reject oversized inputs before fetching them).
type Meta struct {
	ID          string `json:"id"`
	Mime        string `json:"mime"`
	ContentHash string `json:"contentHash"`
	Size        int64  `json:"size"`
}

// Metadata fetches a document's metadata on behalf of the user. The source
// owner-filters on the user subject, so a document the user does not own returns
// not-found there (an HTTPError the caller maps to 404). It never transfers bytes.
func (c *Documents) Metadata(ctx context.Context, id string, obo OnBehalf) (*Meta, error) {
	url := fmt.Sprintf("%s/api/v1/documents/%s", c.baseURL, id)

	var out Meta
	if err := doJSONOnBehalf(ctx, c.doer, "document", c.audience, scopeDocRead, http.MethodGet, url, obo, &out); err != nil {
		return nil, err
	}

	return &out, nil
}

// Content fetches a document's plaintext content bytes on behalf of the user (the
// source decrypts on read). The bytes are sensitive and must never be logged or
// cached unencrypted. Owner-filtering holds end-to-end: a document the user does
// not own returns not-found.
func (c *Documents) Content(ctx context.Context, id string, obo OnBehalf) ([]byte, error) {
	// conduit=render declares the platform purpose: the preview service fetches
	// bytes to rasterize inert page images for in-app viewing — viewing stays
	// available while the chain's signed result is download-frozen mid-workflow
	// (an undeclared consumer fails closed under the freeze).
	url := fmt.Sprintf("%s/api/v1/documents/%s/content?conduit=render", c.baseURL, id)

	return doBytesOnBehalf(ctx, c.doer, "document", c.audience, scopeDocRead, http.MethodGet, url, obo)
}

// ExtractObject fetches one named inner data object out of an ASiC-E container on
// behalf of the user. A multi-file bundle absorbs its originals into the container,
// so the container is their only home; the preview service renders an inner file by
// extracting it here first (an inner file has no document id of its own). Like
// Content it declares conduit=render, so inner-file viewing stays available while the
// chain's signed result is download-frozen mid-workflow. Owner-filtering holds
// end-to-end: a container the user does not own returns not-found.
func (c *Documents) ExtractObject(ctx context.Context, containerID, name string, obo OnBehalf) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/api/v1/documents/%s/data-objects/%s?conduit=render",
		c.baseURL, containerID, neturl.PathEscape(name))

	return doBytesOnBehalf(ctx, c.doer, "document", c.audience, scopeDocRead, http.MethodGet, reqURL, obo)
}
