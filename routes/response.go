package routes

// Manifest is the preview manifest: the page list with per-page dimensions and the
// inert references the caller fetches, plus the text-layer reference. Every
// reference points back to this service; the bytes are images and text only.
type Manifest struct {
	PreviewID    string    `json:"previewId"`
	DocumentID   string    `json:"documentId"`
	Format       string    `json:"format"`
	PageCount    int       `json:"pageCount"`
	Pages        []PageRef `json:"pages"`
	TextLayerRef string    `json:"textLayerRef,omitempty"`
	Renderable   bool      `json:"renderable"`
	ExpiresAt    string    `json:"expiresAt,omitempty"`
}

// PageRef is one page in the manifest: its rendered pixel size and the reference
// to fetch the inert page image.
type PageRef struct {
	Index    int    `json:"index"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	ImageRef string `json:"imageRef"`
}

// NotRenderable is the typed result for a document that cannot be previewed (the
// caller shows "download to review"); it is never an error the UI must guess at.
type NotRenderable struct {
	DocumentID string `json:"documentId"`
	Renderable bool   `json:"renderable"` // always false
	Reason     string `json:"reason"`
	Mime       string `json:"mime"`
}

// TextLayer is the extracted plain-text layer, one entry per page, for screen
// readers and search.
type TextLayer struct {
	DocumentID string   `json:"documentId"`
	Pages      []string `json:"pages"`
}
