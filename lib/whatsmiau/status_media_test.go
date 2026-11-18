package whatsmiau

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

// A JPEG start-of-image marker is enough for content sniffing.
var testJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00, 0x00, 0x01}

// An ftyp box with an mp4 brand, which the sniffer names video/mp4.
var testMP4 = []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0x00, 0x00, 0x00, 0x00, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}

// A QuickTime ftyp box: no mp4 brand, so the sniffer does not name it.
var testMOV = []byte{0x00, 0x00, 0x00, 0x14, 'f', 't', 'y', 'p', 'q', 't', ' ', ' ', 0x00, 0x00, 0x00, 0x00, 'q', 't', ' ', ' '}

// fakeStatusClient records the uploads and the status updates it is asked to send.
type fakeStatusClient struct {
	uploadErr   error
	uploads     [][]byte
	uploadTypes []whatsmeow.MediaType

	sendErr  error
	sendResp whatsmeow.SendResponse
	sentTo   []types.JID
	sent     []*waE2E.Message
}

func (f *fakeStatusClient) Upload(_ context.Context, plaintext []byte, appInfo whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	f.uploads = append(f.uploads, plaintext)
	f.uploadTypes = append(f.uploadTypes, appInfo)
	if f.uploadErr != nil {
		return whatsmeow.UploadResponse{}, f.uploadErr
	}
	return whatsmeow.UploadResponse{
		URL:           "https://mmg.whatsapp.net/v/t62.7118-24/file.enc",
		DirectPath:    "/v/t62.7118-24/file.enc",
		MediaKey:      []byte("media-key"),
		FileEncSHA256: []byte("enc-sha"),
		FileSHA256:    []byte("sha"),
		FileLength:    uint64(len(plaintext)),
	}, nil
}

// SendMessage records a status update sent to status@broadcast.
func (f *fakeStatusClient) SendMessage(_ context.Context, to types.JID, message *waE2E.Message, _ ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	f.sentTo = append(f.sentTo, to)
	f.sent = append(f.sent, message)
	return f.sendResp, f.sendErr
}

// newStatusMediaServer answers like the media URLs a caller may hand over:
// a one-time link that already expired, a missing file, error pages served
// with 200, an empty body and the real media.
func newStatusMediaServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/expired":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGone)
			io.WriteString(w, `{"error":"link expirado"}`)
		case "/missing":
			http.NotFound(w, r)
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write(testJPEG)
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"ok":true}`)
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<!doctype html><html><body>Link expirado</body></html>")
		case "/empty":
		case "/image":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(testJPEG)
		case "/video":
			w.Write(testMP4)
		case "/mov":
			w.Write(testMOV)
		default:
			t.Errorf("unexpected media request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestFetchBytesRejectsHTTPErrors(t *testing.T) {
	srv, _ := newStatusMediaServer(t)
	s := &Whatsmiau{httpClient: srv.Client()}

	for _, path := range []string{"/expired", "/missing", "/broken"} {
		t.Run(path, func(t *testing.T) {
			data, err := s.fetchBytes(context.Background(), srv.URL+path)
			if !errors.Is(err, ErrMediaHTTPStatus) {
				t.Fatalf("error = %v, want ErrMediaHTTPStatus", err)
			}
			if data != nil {
				t.Fatalf("returned the error page as media: %q", data)
			}
			if strings.Contains(err.Error(), srv.URL) {
				t.Fatalf("error carries the URL: %v", err)
			}
		})
	}
	data, err := s.fetchBytes(context.Background(), srv.URL+"/image")
	if err != nil || !bytes.Equal(data, testJPEG) {
		t.Fatalf("2xx download: data=%v err=%v", data, err)
	}
}

func TestFetchBytesLimit(t *testing.T) {
	payload := []byte("0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chunked" {
			// No Content-Length: the cap must hold while reading the body.
			w.Write(payload[:4])
			w.(http.Flusher).Flush()
			w.Write(payload[4:])
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()
	s := &Whatsmiau{httpClient: srv.Client()}
	ctx := context.Background()
	dataURI := "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(payload)

	for name, url := range map[string]string{"declared length": srv.URL + "/sized", "chunked": srv.URL + "/chunked", "data uri": dataURI} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.fetchBytesLimit(ctx, url, int64(len(payload)-1)); !errors.Is(err, ErrMediaTooLarge) {
				t.Fatalf("over the cap: error = %v, want ErrMediaTooLarge", err)
			}
			for _, limit := range []int64{int64(len(payload)), 0} {
				data, err := s.fetchBytesLimit(ctx, url, limit)
				if err != nil || !bytes.Equal(data, payload) {
					t.Fatalf("limit %d: data=%q err=%v", limit, data, err)
				}
			}
		})
	}
}

func TestStatusMediaMatches(t *testing.T) {
	tests := []struct {
		kind string
		data []byte
		want bool
	}{
		{"image", testJPEG, true},
		{"image", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR"), true},
		{"image", []byte(`{"error":"link expirado"}`), false},
		{"image", []byte("<html><body>404</body></html>"), false},
		{"image", testMP4, false},
		{"image", nil, false},
		{"video", testMP4, true},
		{"video", testMOV, true},
		{"video", testJPEG, false},
		{"video", []byte("not really a video"), false},
		{"video", nil, false},
		{"audio", []byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00"), true},
		{"audio", []byte("ID3\x04\x00\x00\x00\x00\x00\x00"), true},
		{"audio", []byte("<html><body>404</body></html>"), false},
		{"audio", nil, false},
		{"sticker", testJPEG, false},
	}
	for _, test := range tests {
		if got := statusMediaMatches(test.kind, test.data); got != test.want {
			t.Errorf("statusMediaMatches(%s, %q) = %v, want %v", test.kind, test.data, got, test.want)
		}
	}
}

func TestStatusMessageOnlyPostsMediaOfTheDeclaredType(t *testing.T) {
	srv, _ := newStatusMediaServer(t)
	s := &Whatsmiau{httpClient: srv.Client()}

	refused := []struct {
		kind, path string
		want       error
	}{
		{"image", "/expired", ErrMediaHTTPStatus},
		{"image", "/missing", ErrMediaHTTPStatus},
		{"image", "/broken", ErrMediaHTTPStatus},
		{"image", "/json", ErrStatusMediaType},
		{"image", "/html", ErrStatusMediaType},
		{"image", "/empty", ErrStatusMediaType},
		{"image", "/video", ErrStatusMediaType},
		{"video", "/expired", ErrMediaHTTPStatus},
		{"video", "/image", ErrStatusMediaType},
		{"video", "/html", ErrStatusMediaType},
		{"audio", "/missing", ErrMediaHTTPStatus},
		{"audio", "/html", ErrStatusMediaType},
	}
	for _, test := range refused {
		t.Run(test.kind+test.path, func(t *testing.T) {
			client := &fakeStatusClient{}
			_, err := s.buildStatusMessage(context.Background(), client, &SendStatusRequest{Type: test.kind, Content: srv.URL + test.path})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if len(client.uploads) != 0 {
				t.Fatalf("uploaded %d files after a bad download", len(client.uploads))
			}
		})
	}

	for kind, paths := range map[string][]string{"image": {"/image"}, "video": {"/video", "/mov"}} {
		for _, path := range paths {
			client := &fakeStatusClient{}
			if _, err := s.buildStatusMessage(context.Background(), client, &SendStatusRequest{Type: kind, Content: srv.URL + path}); err != nil {
				t.Fatalf("%s %s: %v", kind, path, err)
			}
			if len(client.uploads) != 1 {
				t.Fatalf("%s %s: uploaded %d files, want 1", kind, path, len(client.uploads))
			}
		}
	}

	client := &fakeStatusClient{}
	notAnImage := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte(`{"error":"link expirado"}`))
	if _, err := s.buildStatusMessage(context.Background(), client, &SendStatusRequest{Type: "image", Content: notAnImage}); !errors.Is(err, ErrStatusMediaType) {
		t.Fatalf("data URI that is not an image: error = %v, want ErrStatusMediaType", err)
	}
}
