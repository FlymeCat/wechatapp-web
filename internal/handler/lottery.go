package handler

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"wechatapp-web/internal/config"
	"wechatapp-web/internal/lottery"
)

// LotteryHandler serves lottery (大乐透) endpoints: QR-code number recognition
// and win verification.
type LotteryHandler struct {
	decoder *lottery.QRDecoder
	winning *lottery.WinningFetcher
}

// NewLotteryHandler builds the handler with its dependencies.
func NewLotteryHandler(cfg *config.Config) *LotteryHandler {
	return &LotteryHandler{
		decoder: &lottery.QRDecoder{},
		winning: lottery.NewWinningFetcher(cfg.LotteryWinningEndpoint),
	}
}

// ScanResponse is the payload returned by the QR-scan endpoint.
type ScanResponse struct {
	Raw    string              `json:"raw"`    // QR 码解码出的原始文本
	Ticket *lottery.SuperLotto `json:"ticket"` // 识别出的号码
}

// Scan handles:
//
//	POST /api/v1/lottery/scan  (multipart/form-data)
//	  qrcode (file, required): 二维码图片（内容为大乐透号码）
//
//	@Summary      Recognize 大乐透 numbers from a QR code
//	@Description  Decodes a QR-code image and extracts the 大乐透 numbers printed
//	@Description  by the user (5 front numbers + 2 back numbers). The QR payload
//	@Description  must contain 7 numbers, e.g. "05,12,18,23,35,01,08".
//	@Tags         lottery
//	@Accept       multipart/form-data
//	@Produce      json
//	@Param        qrcode formData file true "QR code image containing the ticket numbers"
//	@Success      200 {object} ScanResponse "Decoded raw text and recognized numbers"
//	@Failure      400 {object} ErrorResponse "Missing qrcode, unreadable image, or unparseable numbers"
//	@Failure      422 {object} ErrorResponse "Image decodes but contains no valid QR code"
//	@Router       /lottery/scan [post]
func (h *LotteryHandler) Scan(c *gin.Context) {
	data, err := readFormFile(c, "qrcode", 10<<20)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	text, err := h.decoder.Decode(data)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, fmt.Sprintf("failed to read QR code: %v", err))
		return
	}

	ticket, err := lottery.ParseSuperLotto(text)
	if err != nil {
		if lottery.IsURL(text) {
			writeError(c, http.StatusUnprocessableEntity, fmt.Sprintf(
				"二维码内容是链接而非号码（%s）。真实票面二维码只含校验链接，请改用 /lottery/verify-ticket 提交票面号码", text))
			return
		}
		writeError(c, http.StatusBadRequest, fmt.Sprintf("QR payload is not valid 大乐透 numbers: %v", err))
		return
	}
	ticket.Sort()

	c.JSON(http.StatusOK, ScanResponse{Raw: text, Ticket: ticket})
}

// Verify handles:
//
//	POST /api/v1/lottery/verify  (multipart/form-data)
//	  qrcode         (file, required): 二维码图片（内容为用户打印的号码）
//	  winning_front  (string, required): 开奖前区号码，如 "05,12,18,23,35"
//	  winning_back   (string, required): 开奖后区号码，如 "01,08"
//
//	@Summary      Verify a 大乐透 ticket against winning numbers
//	@Description  Scans the QR code to get the user's printed numbers, then
//	@Description  compares them with the given winning numbers and returns the
//	@Description  prize tier if the ticket wins.
//	@Tags         lottery
//	@Accept       multipart/form-data
//	@Produce      json
//	@Param        qrcode        formData file   true  "QR code image with the user's printed numbers"
//	@Param        winning_front formData string true  "Winning front-area numbers, e.g. 05,12,18,23,35"
//	@Param        winning_back  formData string true  "Winning back-area numbers, e.g. 01,08"
//	@Success      200 {object} lottery.VerifyResult "Match counts and prize (won=false if no prize)"
//	@Failure      400 {object} ErrorResponse "Missing fields or invalid numbers"
//	@Failure      422 {object} ErrorResponse "Image decodes but contains no valid QR code"
//	@Router       /lottery/verify [post]
func (h *LotteryHandler) Verify(c *gin.Context) {
	data, err := readFormFile(c, "qrcode", 10<<20)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	winningFront := c.PostForm("winning_front")
	winningBack := c.PostForm("winning_back")
	if winningFront == "" || winningBack == "" {
		writeError(c, http.StatusBadRequest, `missing required fields "winning_front" and "winning_back"`)
		return
	}

	// Scan the QR code to get the user's printed numbers.
	ticket, err := h.decoder.ScanTicket(data)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, fmt.Sprintf("failed to read QR code: %v", err))
		return
	}
	ticket.Sort()

	result, err := ticket.Verify(winningFront, winningBack)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	c.JSON(http.StatusOK, result)
}

// readFormFile reads a file field from a multipart form with a size limit.
func readFormFile(c *gin.Context, field string, maxBytes int64) ([]byte, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	fh, err := c.FormFile(field)
	if err != nil {
		return nil, fmt.Errorf(`missing required file field %q`, field)
	}
	f, err := fh.Open()
	if err != nil {
		return nil, fmt.Errorf("open uploaded file: %w", err)
	}
	defer f.Close()
	return io.ReadAll(f)
}
