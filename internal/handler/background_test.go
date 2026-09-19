package handler

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"wechatapp-web/internal/config"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// mockRemoveBG returns a transparent PNG for any request, mimicking remove.bg.
func mockRemoveBG(t *testing.T) *httptest.Server {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	// center pixel transparent so compositing is observable
	img.Set(2, 2, color.RGBA{0, 0, 0, 0})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request mirrors the Python reference (multipart + API key header).
		if r.Header.Get("X-Api-Key") != "test-key" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, _, err := r.FormFile("image_file"); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.FormValue("size") != "auto" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(buf.Bytes())
	}))
}

func newTestRouter(t *testing.T) (*gin.Engine, *httptest.Server) {
	t.Helper()
	mock := mockRemoveBG(t)
	cfg := &config.Config{
		RemoveBGAPIKey:   "test-key",
		RemoveBGEndpoint: mock.URL,
		ListenAddr:       ":0",
		MaxImageBytes:    10 << 20,
	}
	r := gin.New()
	bg := NewBackgroundHandler(cfg)
	r.POST("/api/v1/replace-background", bg.ReplaceBackground)
	return r, mock
}

func TestReplaceBackgroundWithColor(t *testing.T) {
	r, mock := newTestRouter(t)
	defer mock.Close()

	// Foreground upload: solid red 4x4.
	fg := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			fg.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var fgBuf bytes.Buffer
	if err := png.Encode(&fgBuf, fg); err != nil {
		t.Fatal(err)
	}

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, err := w.CreateFormFile("image", "test.png")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(fgBuf.Bytes())
	w.WriteField("color", "#00ff00")
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/replace-background", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Center pixel was transparent in the mock cutout; must now be green.
	_, g, b, _ := img.At(2, 2).RGBA()
	if g>>8 <= b>>8 {
		t.Errorf("expected green background at center, g=%d b=%d", g>>8, b>>8)
	}
}

func TestReplaceBackgroundMissingImage(t *testing.T) {
	r, mock := newTestRouter(t)
	defer mock.Close()

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	w.WriteField("color", "#ffffff")
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/replace-background", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestReplaceBackgroundRemoveBGError(t *testing.T) {
	// A mock that returns an error (e.g., bad API key).
	badMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"errors":[{"title":"Authentication failed","detail":"wrong key"}]}`))
	}))
	defer badMock.Close()

	cfg := &config.Config{
		RemoveBGAPIKey:   "bad-key",
		RemoveBGEndpoint: badMock.URL,
		ListenAddr:       ":0",
		MaxImageBytes:    10 << 20,
	}
	r2 := gin.New()
	r2.POST("/api/v1/replace-background", NewBackgroundHandler(cfg).ReplaceBackground)

	fg := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var fgBuf bytes.Buffer
	png.Encode(&fgBuf, fg)

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, _ := w.CreateFormFile("image", "test.png")
	fw.Write(fgBuf.Bytes())
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/replace-background", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r2.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", rec.Code, rec.Body.String())
	}
}
