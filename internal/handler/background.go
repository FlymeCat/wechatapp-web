package handler

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	"github.com/gin-gonic/gin"

	"wechatapp-web/internal/config"
	"wechatapp-web/internal/service"
)

// ErrorResponse is the standard JSON error body returned on failures.
type ErrorResponse struct {
	Error string `json:"error" example:"failed to read uploaded image: ..."`
}

// BackgroundHandler serves the background-replacement endpoint.
type BackgroundHandler struct {
	cfg      *config.Config
	removeBG *service.RemoveBGClient
	composer *service.BackgroundComposer
	maxBytes int64
}

// NewBackgroundHandler builds the handler with its dependencies.
func NewBackgroundHandler(cfg *config.Config) *BackgroundHandler {
	return &BackgroundHandler{
		cfg:      cfg,
		removeBG: service.NewRemoveBGClient(cfg.RemoveBGAPIKey, cfg.RemoveBGEndpoint),
		composer: &service.BackgroundComposer{},
		maxBytes: cfg.MaxImageBytes,
	}
}

// ReplaceBackground removes the background of an uploaded image via the
// remove.bg API and optionally composites the cutout onto a new background.
//
//	POST /api/v1/replace-background  (multipart/form-data)
//	  image      (file, required)  : the source image whose background is removed
//	  background (file, optional)  : an image placed behind the cutout
//	  color      (string, optional): hex color background, e.g. #ffffff
//	  size       (string, optional): remove.bg size, default "auto"
//	  format     (string, optional): output "png" (default) or "jpeg"
//
// The response body is the resulting image.
//
//	@Summary      Replace image background
//	@Description  Uploads an image, removes its background with the remove.bg API,
//	@Description  then optionally replaces the background with a solid color or another
//	@Description  image. Returns the processed image directly as the response body.
//	@Tags         background
//	@Accept       multipart/form-data
//	@Produce      image/png
//	@Produce      image/jpeg
//	@Param        image      formData file   true  "Source image whose background will be removed"
//	@Param        background formData file   false "Background image to place behind the cutout (wins over color)"
//	@Param        color      formData string false "Hex background color, e.g. #00aaff or #fff"
//	@Param        size       formData string false "remove.bg size parameter" default(auto)
//	@Param        format     formData string false "Output image format" default(png) Enums(png, jpeg)
//	@Success      200 {file} binary "Processed image (PNG or JPEG)"
//	@Failure      400 {object} ErrorResponse "Bad request: missing image, unreadable file, or invalid color"
//	@Failure      502 {object} ErrorResponse "remove.bg upstream API error (e.g. invalid API key)"
//	@Failure      500 {object} ErrorResponse "Internal error while composing the result"
//	@Router       /replace-background [post]
func (h *BackgroundHandler) ReplaceBackground(c *gin.Context) {
	// Guard against oversized uploads.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBytes)

	imageFile, err := c.FormFile("image")
	if err != nil {
		writeError(c, http.StatusBadRequest, `missing required file field "image"`)
		return
	}

	imageData, err := readUploadedFile(imageFile)
	if err != nil {
		writeError(c, http.StatusBadRequest, fmt.Sprintf("failed to read uploaded image: %v", err))
		return
	}

	// Optional background image.
	var backgroundData []byte
	if bgFile, err := c.FormFile("background"); err == nil {
		backgroundData, err = readUploadedFile(bgFile)
		if err != nil {
			writeError(c, http.StatusBadRequest, fmt.Sprintf("failed to read background image: %v", err))
			return
		}
	}

	color := c.PostForm("color")
	size := c.PostForm("size")
	format := c.PostForm("format")

	// 1. Remove the background via the remove.bg API (mirrors the Python reference).
	result, err := h.removeBG.Remove(c.Request.Context(), imageData, imageFile.Filename, size)
	if err != nil {
		writeError(c, http.StatusBadGateway, err.Error())
		return
	}

	// 2. Optionally replace the background with a color or another image.
	out, err := h.composer.Compose(service.ComposeOptions{
		ForegroundPNG:   result.Data,
		BackgroundImage: backgroundData,
		BackgroundColor: color,
		ResultFormat:    format,
	})
	if err != nil {
		writeError(c, http.StatusInternalServerError, fmt.Sprintf("failed to compose result: %v", err))
		return
	}

	// 3. Stream the image back.
	contentType := mime.TypeByExtension(".png")
	if format == "jpeg" || format == "jpg" {
		contentType = mime.TypeByExtension(".jpg")
	}
	c.Data(http.StatusOK, contentType, out)
}

func writeError(c *gin.Context, status int, msg string) {
	c.JSON(status, ErrorResponse{Error: msg})
}

func readUploadedFile(fh *multipart.FileHeader) ([]byte, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
