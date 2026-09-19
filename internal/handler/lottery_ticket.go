package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"wechatapp-web/internal/lottery"
)

// NumberList accepts either a JSON array of integers ([4,10,12,16,20]) or a
// delimited string ("04,10,12,16,20"), so clients can send whatever their
// input/OCR step produced.
type NumberList []int

// UnmarshalJSON implements json.Unmarshaler.
func (n *NumberList) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*n = nil
		return nil
	}
	switch data[0] {
	case '[':
		var ints []int
		if err := json.Unmarshal(data, &ints); err != nil {
			return fmt.Errorf("expected an array of integers: %w", err)
		}
		*n = ints
		return nil
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		nums, err := lottery.ExtractNumbers(s)
		if err != nil {
			return err
		}
		*n = nums
		return nil
	default:
		return fmt.Errorf("expected an array of integers or a string of numbers")
	}
}

// BetInput is one 大乐透 bet. It accepts either an object
// ({"front":[4,10,12,16,20],"back":[10,12]}), an object with string fields
// ({"front":"04,10,12,16,20","back":"10,12"}), or a single string
// ("04 10 12 16 20 + 10 12").
type BetInput struct {
	Front NumberList `json:"front" example:"4,10,12,16,20"` // 前区 5 个号码
	Back  NumberList `json:"back" example:"10,12"`          // 后区 2 个号码
}

// UnmarshalJSON implements json.Unmarshaler.
func (b *BetInput) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		bet, err := lottery.ParseSuperLotto(s)
		if err != nil {
			return err
		}
		bet.Sort()
		b.Front, b.Back = bet.Front, bet.Back
		return nil
	}
	type plain BetInput // avoid recursion
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*b = BetInput(p)
	return nil
}

// TicketInput is the shared client input for parsing and verification.
type TicketInput struct {
	Issue string     `json:"issue" example:"26102"` // 期号（可选）
	Bets  []BetInput `json:"bets"`                  // 票面号码；与 text 二选一
	Text  string     `json:"text"`                  // 整票文本，一行一注；bets 为空时使用
}

// VerifyTicketRequest is the JSON body of POST /lottery/verify-ticket.
type VerifyTicketRequest struct {
	TicketInput
	WinningFront string `json:"winning_front" example:"01,03,07,27,28"` // 开奖前区（可选）
	WinningBack  string `json:"winning_back" example:"06,07"`           // 开奖后区（可选）
}

// ParseTicketResponse is the normalized ticket returned for user confirmation.
type ParseTicketResponse struct {
	Issue     string               `json:"issue,omitempty"` // 识别出的期号
	TotalBets int                  `json:"total_bets"`      // 注数
	Bets      []lottery.SuperLotto `json:"bets"`            // 规范化后的号码
}

// buildTicket normalizes client input into a validated multi-bet Ticket.
func buildTicket(in *TicketInput) (*lottery.Ticket, error) {
	var t *lottery.Ticket

	switch {
	case len(in.Bets) > 0:
		t = &lottery.Ticket{Issue: strings.TrimSpace(in.Issue)}
		for i := range in.Bets {
			bet, err := lottery.NewBet(in.Bets[i].Front, in.Bets[i].Back)
			if err != nil {
				return nil, fmt.Errorf("第 %d 注：%v", i+1, err)
			}
			t.Bets = append(t.Bets, *bet)
		}
	case strings.TrimSpace(in.Text) != "":
		parsed, err := lottery.ParseTicketText(in.Text)
		if err != nil {
			return nil, err
		}
		t = parsed
		if t.Issue == "" {
			t.Issue = strings.TrimSpace(in.Issue)
		}
	default:
		return nil, fmt.Errorf("bets 或 text 至少提供一个")
	}

	if len(t.Bets) == 0 {
		return nil, fmt.Errorf("未解析出任何号码")
	}
	return t, nil
}

// ParseTicket handles:
//
//	POST /api/v1/lottery/parse-ticket  (application/json)
//
//	@Summary      Parse and normalize 大乐透 ticket numbers
//	@Description  Validates client-supplied ticket numbers (from manual input or
//	@Description  on-device OCR) and returns them normalized, so the app can show
//	@Description  a confirmation screen before calling /lottery/verify-ticket.
//	@Tags         lottery
//	@Accept       json
//	@Produce      json
//	@Param        request body TicketInput true "Ticket numbers as bets or text"
//	@Success      200 {object} ParseTicketResponse "Normalized issue and bets"
//	@Failure      400 {object} ErrorResponse "Missing input or invalid numbers (message names the offending bet/line)"
//	@Router       /lottery/parse-ticket [post]
func (h *LotteryHandler) ParseTicket(c *gin.Context) {
	var in TicketInput
	if err := c.ShouldBindJSON(&in); err != nil {
		writeError(c, http.StatusBadRequest, fmt.Sprintf("请求体解析失败：%v", err))
		return
	}

	t, err := buildTicket(&in)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	c.JSON(http.StatusOK, ParseTicketResponse{
		Issue:     t.Issue,
		TotalBets: len(t.Bets),
		Bets:      t.Bets,
	})
}

// VerifyTicket handles:
//
//	POST /api/v1/lottery/verify-ticket  (application/json)
//
//	@Summary      Verify a multi-bet 大乐透 ticket
//	@Description  Verifies a printed 大乐透 ticket that may contain several bets
//	@Description  (单式票通常 5 注). Numbers may be sent as structured bets or as
//	@Description  multi-line text. Winning numbers can be supplied directly; if
//	@Description  omitted and an issue is given, they are fetched from the
//	@Description  official China Sports Lottery draw-history API.
//	@Tags         lottery
//	@Accept       json
//	@Produce      json
//	@Param        request body VerifyTicketRequest true "Ticket bets and optional winning numbers"
//	@Success      200 {object} lottery.TicketVerifyResult "Per-bet match counts, prize and totals"
//	@Failure      400 {object} ErrorResponse "Invalid numbers or missing winning numbers/issue"
//	@Failure      502 {object} ErrorResponse "Failed to fetch official winning numbers"
//	@Router       /lottery/verify-ticket [post]
func (h *LotteryHandler) VerifyTicket(c *gin.Context) {
	var req VerifyTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, fmt.Sprintf("请求体解析失败：%v", err))
		return
	}

	ticket, err := buildTicket(&req.TicketInput)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	// Resolve winning numbers: explicit values win, otherwise fetch by issue.
	var winning *lottery.WinningNumbers
	switch {
	case req.WinningFront != "" && req.WinningBack != "":
		w, err := lottery.ParseWinningNumbers(req.WinningFront, req.WinningBack)
		if err != nil {
			writeError(c, http.StatusBadRequest, err.Error())
			return
		}
		winning = w
	case ticket.Issue != "":
		w, err := h.winning.Fetch(c.Request.Context(), ticket.Issue)
		if err != nil {
			if errors.Is(err, lottery.ErrIssueNotFound) {
				writeError(c, http.StatusNotFound, err.Error())
				return
			}
			writeError(c, http.StatusBadGateway, fmt.Sprintf("failed to fetch winning numbers: %v", err))
			return
		}
		winning = w
	default:
		writeError(c, http.StatusBadRequest,
			"请提供 winning_front + winning_back，或提供 issue 以自动获取开奖号码")
		return
	}

	result, err := ticket.Verify(winning)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

// Winning handles:
//
//	GET /api/v1/lottery/winning/:issue
//
//	@Summary      Get official 大乐透 winning numbers
//	@Description  Fetches the official draw result for the given issue from the
//	@Description  China Sports Lottery API (cached in memory).
//	@Tags         lottery
//	@Produce      json
//	@Param        issue path string true "Issue number, e.g. 26102"
//	@Success      200 {object} lottery.WinningNumbers "Official draw result"
//	@Failure      404 {object} ErrorResponse "Issue not found"
//	@Failure      502 {object} ErrorResponse "Upstream API error"
//	@Router       /lottery/winning/{issue} [get]
func (h *LotteryHandler) Winning(c *gin.Context) {
	issue := c.Param("issue")
	w, err := h.winning.Fetch(c.Request.Context(), issue)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, lottery.ErrIssueNotFound) {
			status = http.StatusNotFound
		}
		writeError(c, status, err.Error())
		return
	}
	c.JSON(http.StatusOK, w)
}
