package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxDiscordAttachmentFileBytes = 20 * 1024 * 1024
	maxDiscordAttachmentsMessage  = 5
	maxDiscordAttachmentsTask     = 100 * 1024 * 1024
)

var discordAttachmentHosts = map[string]bool{
	"cdn.discordapp.com":   true,
	"media.discordapp.net": true,
}

type attachmentDownloader struct {
	client      *http.Client
	allowedHost map[string]bool
	allowHTTP   bool
	maxBytes    int64
}

type downloadedAttachment struct {
	ContentType string
	SizeBytes   int64
	SHA256      string
}

func defaultAttachmentDownloader() attachmentDownloader {
	return newAttachmentDownloader(&http.Client{Timeout: 15 * time.Second}, discordAttachmentHosts, false, maxDiscordAttachmentFileBytes)
}

func newAttachmentDownloader(client *http.Client, allowedHost map[string]bool, allowHTTP bool, maxBytes int64) attachmentDownloader {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	copyClient := *client
	if copyClient.Timeout == 0 {
		copyClient.Timeout = 15 * time.Second
	}
	hosts := map[string]bool{}
	for host, allowed := range allowedHost {
		hosts[strings.ToLower(strings.TrimSpace(host))] = allowed
	}
	downloader := attachmentDownloader{
		client:      &copyClient,
		allowedHost: hosts,
		allowHTTP:   allowHTTP,
		maxBytes:    maxBytes,
	}
	downloader.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return downloader.validateURL(req.URL)
	}
	return downloader
}

func (d attachmentDownloader) download(ctx context.Context, sourceURL, finalPath string) (downloadedAttachment, error) {
	parsed, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil {
		return downloadedAttachment{}, fmt.Errorf("parse attachment URL: %w", err)
	}
	if err := d.validateURL(parsed); err != nil {
		return downloadedAttachment{}, err
	}
	if d.maxBytes <= 0 {
		d.maxBytes = maxDiscordAttachmentFileBytes
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o700); err != nil {
		return downloadedAttachment{}, fmt.Errorf("create attachment directory: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return downloadedAttachment{}, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return downloadedAttachment{}, fmt.Errorf("download attachment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return downloadedAttachment{}, fmt.Errorf("download attachment: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > d.maxBytes {
		return downloadedAttachment{}, fmt.Errorf("attachment exceeds %d bytes", d.maxBytes)
	}
	tmp, err := os.CreateTemp(filepath.Dir(finalPath), ".download-*")
	if err != nil {
		return downloadedAttachment{}, fmt.Errorf("create temp attachment: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	hash := sha256.New()
	var first bytes.Buffer
	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > d.maxBytes {
				_ = tmp.Close()
				return downloadedAttachment{}, fmt.Errorf("attachment exceeds %d bytes", d.maxBytes)
			}
			chunk := buf[:n]
			if first.Len() < 512 {
				need := 512 - first.Len()
				if len(chunk) < need {
					need = len(chunk)
				}
				_, _ = first.Write(chunk[:need])
			}
			if _, err := hash.Write(chunk); err != nil {
				_ = tmp.Close()
				return downloadedAttachment{}, err
			}
			if _, err := tmp.Write(chunk); err != nil {
				_ = tmp.Close()
				return downloadedAttachment{}, fmt.Errorf("write attachment: %w", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = tmp.Close()
			return downloadedAttachment{}, fmt.Errorf("read attachment: %w", readErr)
		}
	}
	contentType := sniffSupportedImage(first.Bytes())
	if contentType == "" {
		_ = tmp.Close()
		return downloadedAttachment{}, fmt.Errorf("unsupported attachment content type")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return downloadedAttachment{}, fmt.Errorf("sync attachment: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return downloadedAttachment{}, fmt.Errorf("close attachment: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return downloadedAttachment{}, fmt.Errorf("store attachment: %w", err)
	}
	return downloadedAttachment{ContentType: contentType, SizeBytes: written, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func (d attachmentDownloader) validateURL(parsed *url.URL) error {
	if parsed == nil {
		return fmt.Errorf("attachment URL is required")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && !(d.allowHTTP && scheme == "http") {
		return fmt.Errorf("attachment URL must use HTTPS")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || !d.allowedHost[host] {
		return fmt.Errorf("attachment host is not allowed")
	}
	return nil
}

func sniffSupportedImage(data []byte) string {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}
	if len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))) {
		return "image/gif"
	}
	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return "image/webp"
	}
	return ""
}
