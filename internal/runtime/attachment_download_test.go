package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

var tinyPNG = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
var tinyOgg = []byte{'O', 'g', 'g', 'S', 0, 2, 0, 0, 0, 0, 0, 0}

func TestAttachmentDownloaderStoresSupportedImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tinyPNG)
	}))
	defer server.Close()
	downloader := newAttachmentDownloader(server.Client(), map[string]bool{testServerHost(t, server.URL): true}, true, 1024)
	finalPath := filepath.Join(t.TempDir(), "screen.png")
	downloaded, err := downloader.download(context.Background(), server.URL+"/screen.png", finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if downloaded.ContentType != "image/png" || downloaded.SizeBytes != int64(len(tinyPNG)) || downloaded.SHA256 == "" {
		t.Fatalf("downloaded = %#v", downloaded)
	}
	if _, err := os.Stat(finalPath); err != nil {
		t.Fatal(err)
	}
}

func TestAttachmentDownloaderStoresDiscordVoiceMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/ogg")
		_, _ = w.Write(tinyOgg)
	}))
	defer server.Close()
	downloader := newAttachmentDownloader(server.Client(), map[string]bool{testServerHost(t, server.URL): true}, true, 1024)
	finalPath := filepath.Join(t.TempDir(), "voice-message.ogg")
	downloaded, err := downloader.download(context.Background(), server.URL+"/voice-message.ogg", finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if downloaded.ContentType != "audio/ogg" || downloaded.SizeBytes != int64(len(tinyOgg)) || downloaded.SHA256 == "" {
		t.Fatalf("downloaded = %#v", downloaded)
	}
	if _, err := os.Stat(finalPath); err != nil {
		t.Fatal(err)
	}
}

func TestAttachmentDownloaderStoresTextFile(t *testing.T) {
	content := []byte("DRAFTS - xgb-valid-manifold-interpolation\nMODE: auto\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(content)
	}))
	defer server.Close()
	downloader := newAttachmentDownloader(server.Client(), map[string]bool{testServerHost(t, server.URL): true}, true, 1024)
	finalPath := filepath.Join(t.TempDir(), "message.txt")
	downloaded, err := downloader.download(context.Background(), server.URL+"/message.txt", finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if downloaded.ContentType != "text/plain; charset=utf-8" || downloaded.SizeBytes != int64(len(content)) || downloaded.SHA256 == "" {
		t.Fatalf("downloaded = %#v", downloaded)
	}
	stored, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(content) {
		t.Fatalf("stored text = %q, want %q", stored, content)
	}
}

func TestAttachmentDownloaderRejectsRedirectOutsideAllowedHosts(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/screen.png", http.StatusFound)
	}))
	defer redirect.Close()
	downloader := newAttachmentDownloader(redirect.Client(), map[string]bool{testServerHost(t, redirect.URL): true}, true, 1024)
	if _, err := downloader.download(context.Background(), redirect.URL+"/screen.png", filepath.Join(t.TempDir(), "screen.png")); err == nil {
		t.Fatal("download accepted redirect outside allowed hosts")
	}
}

func TestAttachmentDownloaderRejectsOversizedFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(append(tinyPNG, make([]byte, 100)...))
	}))
	defer server.Close()
	downloader := newAttachmentDownloader(server.Client(), map[string]bool{testServerHost(t, server.URL): true}, true, int64(len(tinyPNG)))
	if _, err := downloader.download(context.Background(), server.URL+"/screen.png", filepath.Join(t.TempDir(), "screen.png")); err == nil {
		t.Fatal("download accepted oversized attachment")
	}
}

func testServerHost(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Hostname()
}
