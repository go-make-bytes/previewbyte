package render

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/go-quicktest/qt"
)

// buildZip constructs an in-memory zip with the given entries, for testing the
// directory-listing-only OOXML disambiguation without needing a real Office file.
func buildZip(entries map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(content))
	}
	_ = zw.Close()

	return buf.Bytes()
}

func TestSniffDocx(t *testing.T) {
	z := buildZip(map[string]string{
		"[Content_Types].xml": "<Types/>",
		"word/document.xml":   "<w:document/>",
	})
	qt.Assert(t, qt.Equals(Sniff(z), MimeDocx))
}

func TestSniffXlsx(t *testing.T) {
	z := buildZip(map[string]string{
		"[Content_Types].xml": "<Types/>",
		"xl/workbook.xml":     "<workbook/>",
	})
	qt.Assert(t, qt.Equals(Sniff(z), MimeXlsx))
}

func TestSniffPptx(t *testing.T) {
	z := buildZip(map[string]string{
		"[Content_Types].xml":  "<Types/>",
		"ppt/presentation.xml": "<presentation/>",
	})
	qt.Assert(t, qt.Equals(Sniff(z), MimePptx))
}

// A plain zip with no OOXML marker entry stays the generic application/zip — an
// ASiC-E container or an ordinary .zip must not be misidentified as an Office
// document.
func TestSniffPlainZipStaysGeneric(t *testing.T) {
	z := buildZip(map[string]string{"hello.txt": "just a zip, not an office document"})
	qt.Assert(t, qt.Equals(Sniff(z), "application/zip"))
}

// Non-zip content is unaffected by the OOXML disambiguation.
func TestSniffNonZipUnaffected(t *testing.T) {
	qt.Assert(t, qt.Equals(Sniff([]byte("plain text content")), "text/plain"))
	qt.Assert(t, qt.Equals(Sniff([]byte("%PDF-1.4\n")), "application/pdf"))
}
