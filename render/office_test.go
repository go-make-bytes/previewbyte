package render

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-quicktest/qt"
)

func officeTestCfg(converterURL string, maxBytes int64) Config {
	return Config{
		PoolSize: 1, MaxDPI: 150, MaxWidth: 2048, ImageFormat: "png",
		Timeout: 20 * time.Second, MaxPages: 100, InputMaxBytes: maxBytes,
		OfficeConverterURL:     converterURL,
		OfficeConverterTimeout: 5 * time.Second,
	}
}

// A .docx (any bytes — the stub converter below ignores the input and always
// returns a fixed PDF, since this test is about the office backend's own
// convert-then-delegate logic, not a real conversion) sniffed to MimeDocx.
var docxInput = Input{Bytes: []byte("not real OOXML bytes, the stub ignores them"), Mime: MimeDocx}

// stubConverter runs a fake Gotenberg that always answers the LibreOffice convert
// route the same way, so tests exercise the office backend's own logic against a
// controlled response rather than a real conversion.
func stubConverter(t *testing.T, status int, contentType string, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qt.Check(t, qt.Equals(r.URL.Path, "/forms/libreoffice/convert"))
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	return srv
}

// A successful conversion delegates every call to the real PDFium backend on the
// converted bytes — proving the office backend duplicates no render logic.
func TestOfficeConvertsAndDelegates(t *testing.T) {
	pdf := testRenderer(t)
	defer pdf.Close()
	srv := stubConverter(t, http.StatusOK, "application/pdf", samplePDF())
	office := NewOffice(officeTestCfg(srv.URL, 64<<20), pdf)
	ctx := context.Background()

	doc, err := office.Inspect(ctx, docxInput)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(doc.Format, "office"))
	qt.Assert(t, qt.Equals(doc.PageCount, 1))

	img, err := office.RenderPage(ctx, docxInput, 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(img.ContentType, "image/png"))
	qt.Assert(t, qt.IsTrue(len(img.Bytes) > 0))

	texts, err := office.Text(ctx, docxInput)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(len(texts), 1))
	qt.Assert(t, qt.IsTrue(strings.Contains(texts[0], "previewbyte")))
}

// A MIME the office backend doesn't handle (dispatch would never route this here,
// but the backend itself must still fail safe) is unsupported, not a converter call.
func TestOfficeRejectsNonOfficeMime(t *testing.T) {
	pdf := testRenderer(t)
	defer pdf.Close()
	srv := stubConverter(t, http.StatusOK, "application/pdf", samplePDF())
	office := NewOffice(officeTestCfg(srv.URL, 64<<20), pdf)

	_, err := office.Inspect(context.Background(), Input{Bytes: []byte("x"), Mime: "application/pdf"})
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrUnsupportedFormat)))
}

// The conversion request always asks for one page per sheet, sized to that
// sheet's content — otherwise a spreadsheet with a small used range converts onto
// its full default paper size and the data renders as a speck in a mostly blank
// page (found live, verified against a real Gotenberg before relying on this).
func TestOfficeRequestsSinglePageSheets(t *testing.T) {
	pdf := testRenderer(t)
	defer pdf.Close()

	var gotField string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		qt.Check(t, qt.IsNil(r.ParseMultipartForm(1<<20))) //nolint:gosec // body already bounded by MaxBytesReader above; a test server reading its own small fixture, not a production handler
		gotField = r.FormValue("singlePageSheets")
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(samplePDF())
	}))
	t.Cleanup(srv.Close)
	office := NewOffice(officeTestCfg(srv.URL, 64<<20), pdf)

	_, err := office.Inspect(context.Background(), docxInput)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(gotField, "true"))
}

// A non-2xx from the converter is the same safe "couldn't render this one" outcome
// every other backend uses for an open/parse failure.
func TestOfficeNon2xxIsUnsupported(t *testing.T) {
	pdf := testRenderer(t)
	defer pdf.Close()
	srv := stubConverter(t, http.StatusInternalServerError, "text/plain", []byte("converter blew up"))
	office := NewOffice(officeTestCfg(srv.URL, 64<<20), pdf)

	_, err := office.Inspect(context.Background(), docxInput)
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrUnsupportedFormat)))
}

// A 200 whose body isn't actually a PDF once re-sniffed (the declared
// Content-Type is never trusted, same as every other input) is also unsupported.
func TestOfficeNonPDFResponseIsUnsupported(t *testing.T) {
	pdf := testRenderer(t)
	defer pdf.Close()
	srv := stubConverter(t, http.StatusOK, "application/pdf", []byte("this is not a pdf despite the header"))
	office := NewOffice(officeTestCfg(srv.URL, 64<<20), pdf)

	_, err := office.Inspect(context.Background(), docxInput)
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrUnsupportedFormat)))
}

// A converted response larger than the byte cap is rejected before it reaches the
// PDF backend at all.
func TestOfficeOversizedResponseIsTooLarge(t *testing.T) {
	pdf := testRenderer(t)
	defer pdf.Close()
	huge := append([]byte("%PDF-1.4\n"), make([]byte, 1000)...)
	srv := stubConverter(t, http.StatusOK, "application/pdf", huge)
	office := NewOffice(officeTestCfg(srv.URL, 100), pdf) // cap far below the response size

	_, err := office.Inspect(context.Background(), docxInput)
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrTooLarge)))
}

// An unreachable converter is ErrUpstreamUnavailable — "try again," not "download
// to review": the input was never even evaluated.
func TestOfficeUnreachableIsUpstreamUnavailable(t *testing.T) {
	pdf := testRenderer(t)
	defer pdf.Close()
	cfg := officeTestCfg("http://127.0.0.1:1", 64<<20) // nothing listens here
	cfg.OfficeConverterTimeout = 2 * time.Second
	office := NewOffice(cfg, pdf)

	_, err := office.Inspect(context.Background(), docxInput)
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrUpstreamUnavailable)))
}

// TestOfficeRequestTargetIgnoresDocumentContent is the SSRF-bait proof: the
// conversion request's destination is a fixed value from Config, never anything
// read out of the document bytes. A malicious document cannot smuggle a URL,
// a cloud-metadata address, or a file:// reference into where the outbound HTTP
// call actually goes — no matter what the bytes contain, every request lands on
// the same configured Gotenberg endpoint. (What Gotenberg itself might fetch from
// *inside* a converted document — e.g. a remote image reference — is a Gotenberg/
// LibreOffice-side risk outside this service's own code; that's the case for the
// deploy-time network egress-block, not something this test can prove.)
func TestOfficeRequestTargetIgnoresDocumentContent(t *testing.T) {
	pdf := testRenderer(t)
	defer pdf.Close()

	var gotHost, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotPath = r.Host, r.URL.Path
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(samplePDF())
	}))
	t.Cleanup(srv.Close)
	office := NewOffice(officeTestCfg(srv.URL, 64<<20), pdf)

	payloads := [][]byte{
		[]byte("http://attacker.example/steal"),
		[]byte(`<Relationship Target="http://169.254.169.254/latest/meta-data/"/>`),
		[]byte("file:///etc/passwd"),
		{0x00, 0x01, 0x02},
	}
	for _, p := range payloads {
		gotHost, gotPath = "", ""
		_, _ = office.Inspect(context.Background(), Input{Bytes: p, Mime: MimeDocx})
		qt.Assert(t, qt.Equals(gotPath, "/forms/libreoffice/convert"))
		qt.Assert(t, qt.Equals("http://"+gotHost, srv.URL))
	}
}

func TestOfficeReadyAndClose(t *testing.T) {
	pdf := testRenderer(t)
	defer pdf.Close()
	office := NewOffice(officeTestCfg("http://unused.invalid", 64<<20), pdf)

	qt.Assert(t, qt.IsNil(office.Ready(context.Background())))
	office.Close() // no-op; must not panic
}
