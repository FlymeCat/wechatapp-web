package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"wechatapp-web/internal/config"
)

func newLotteryRouter(t *testing.T, winningEndpoint string) *gin.Engine {
	t.Helper()
	cfg := &config.Config{
		LotteryWinningEndpoint: winningEndpoint,
		ListenAddr:             ":0",
		MaxImageBytes:          10 << 20,
	}
	r := gin.New()
	lotto := NewLotteryHandler(cfg)
	r.POST("/api/v1/lottery/parse-ticket", lotto.ParseTicket)
	r.POST("/api/v1/lottery/verify-ticket", lotto.VerifyTicket)
	r.GET("/api/v1/lottery/winning/:issue", lotto.Winning)
	return r
}

func postRawJSON(t *testing.T, r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// mockWinningAPI serves the official API shape for issue 26102.
func mockWinningAPI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"value": map[string]any{
				"list": []map[string]any{{
					"lotteryDrawNum":       "26102",
					"lotteryDrawTime":      "2026-09-07",
					"lotteryDrawResult":    "01 03 07 27 28 06 07",
					"poolBalanceAfterdraw": "725,321,799.11",
				}},
			},
		})
	}))
}

func postJSON(t *testing.T, r *gin.Engine, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestVerifyTicketRealTicketWithoutPrize replays the user's real ticket.
func TestVerifyTicketRealTicketExplicitWinning(t *testing.T) {
	r := newLotteryRouter(t, "")

	body := VerifyTicketRequest{
		TicketInput: TicketInput{
			Issue: "26102",
			Bets: []BetInput{
				{Front: []int{4, 10, 12, 16, 20}, Back: []int{10, 12}},
				{Front: []int{2, 20, 28, 33, 35}, Back: []int{1, 9}},
				{Front: []int{11, 13, 20, 25, 34}, Back: []int{3, 12}},
				{Front: []int{9, 11, 15, 28, 29}, Back: []int{7, 12}},
				{Front: []int{2, 18, 19, 21, 31}, Back: []int{5, 6}},
			},
		},
		WinningFront: "01,03,07,27,28",
		WinningBack:  "06,07",
	}
	rec := postJSON(t, r, "/api/v1/lottery/verify-ticket", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var res struct {
		TotalBets   int  `json:"total_bets"`
		WinningBets int  `json:"winning_bets"`
		TotalWon    bool `json:"total_won"`
		Bets        []struct {
			MatchedFront int `json:"matched_front"`
			MatchedBack  int `json:"matched_back"`
		} `json:"bets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.TotalBets != 5 || res.WinningBets != 0 || res.TotalWon {
		t.Errorf("got total=%d winning=%d won=%v; want 5/0/false", res.TotalBets, res.WinningBets, res.TotalWon)
	}
	wantF := []int{0, 1, 0, 1, 0}
	wantB := []int{0, 0, 0, 1, 1}
	for i := range res.Bets {
		if res.Bets[i].MatchedFront != wantF[i] || res.Bets[i].MatchedBack != wantB[i] {
			t.Errorf("bet #%d = %d+%d, want %d+%d", i+1,
				res.Bets[i].MatchedFront, res.Bets[i].MatchedBack, wantF[i], wantB[i])
		}
	}
}

// TestVerifyTicketAutoFetchWinning uses the issue number to fetch the draw.
func TestVerifyTicketAutoFetchWinning(t *testing.T) {
	mock := mockWinningAPI(t)
	defer mock.Close()
	r := newLotteryRouter(t, mock.URL)

	body := VerifyTicketRequest{
		TicketInput: TicketInput{
			Issue: "26102",
			Bets:  []BetInput{{Front: []int{1, 3, 9, 11, 13}, Back: []int{6, 9}}}, // 2+1 -> 七等奖
		},
	}
	rec := postJSON(t, r, "/api/v1/lottery/verify-ticket", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Winning struct {
			Issue string `json:"issue"`
			Front []int  `json:"front"`
		} `json:"winning"`
		WinningBets int `json:"winning_bets"`
		Bets        []struct {
			Won    bool   `json:"won"`
			Amount string `json:"amount"`
			Prize  *struct {
				Tier     int    `json:"tier"`
				TierName string `json:"tier_name"`
			} `json:"prize"`
		} `json:"bets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Winning.Issue != "26102" {
		t.Errorf("winning.issue = %q, want 26102", res.Winning.Issue)
	}
	if res.WinningBets != 1 || !res.Bets[0].Won {
		t.Fatalf("expected 1 winning bet, got %d (won=%v)", res.WinningBets, res.Bets[0].Won)
	}
	if res.Bets[0].Prize == nil || res.Bets[0].Prize.Tier != 7 {
		t.Errorf("expected 七等奖, got %+v", res.Bets[0].Prize)
	}
	// Pool below 8亿 (725,321,799.11) -> 七等奖 = 5元.
	if res.Bets[0].Amount != "5元" {
		t.Errorf("amount = %q, want 5元", res.Bets[0].Amount)
	}
}

func TestVerifyTicketBadRequests(t *testing.T) {
	mock := mockWinningAPI(t)
	defer mock.Close()
	r := newLotteryRouter(t, mock.URL)

	cases := []struct {
		name string
		body VerifyTicketRequest
	}{
		{"no bets", VerifyTicketRequest{TicketInput: TicketInput{Issue: "26102"}}},
		{"no winning numbers and no issue", VerifyTicketRequest{TicketInput: TicketInput{Bets: []BetInput{{Front: []int{1, 2, 3, 4, 5}, Back: []int{1, 2}}}}}},
		{"out of range", VerifyTicketRequest{TicketInput: TicketInput{Issue: "26102", Bets: []BetInput{{Front: []int{1, 2, 3, 4, 99}, Back: []int{1, 2}}}}}},
		{"short bet", VerifyTicketRequest{TicketInput: TicketInput{Issue: "26102", Bets: []BetInput{{Front: []int{1, 2, 3}, Back: []int{1, 2}}}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := postJSON(t, r, "/api/v1/lottery/verify-ticket", c.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestParseTicketConfirmFlow covers the client "input & confirm" step.
func TestParseTicketConfirmFlow(t *testing.T) {
	mock := mockWinningAPI(t)
	defer mock.Close()
	r := newLotteryRouter(t, mock.URL)

	// Whole ticket pasted as text -> normalized bets for the confirm screen.
	text := `第 26102期
2026年09月07日开奖
910330-292261-116430-816911 057908 BA4Wbg
单式票 1倍 合计10元
① 04 10 12 16 20 + 10 12
② 02 20 28 33 35 + 01 09`
	rec := postRawJSON(t, r, "/api/v1/lottery/parse-ticket",
		`{"text": `+jsonString(text)+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var parsed struct {
		Issue     string `json:"issue"`
		TotalBets int    `json:"total_bets"`
		Bets      []struct {
			Front []int `json:"front"`
			Back  []int `json:"back"`
		} `json:"bets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Issue != "26102" || parsed.TotalBets != 2 {
		t.Fatalf("issue=%q total=%d, want 26102/2", parsed.Issue, parsed.TotalBets)
	}
	if len(parsed.Bets[0].Front) != 5 || parsed.Bets[0].Back[1] != 12 {
		t.Errorf("bet #1 = %+v", parsed.Bets[0])
	}
}

func TestVerifyTicketFlexibleInput(t *testing.T) {
	mock := mockWinningAPI(t)
	defer mock.Close()
	r := newLotteryRouter(t, mock.URL)

	// Bet as a single string; front/back as delimited strings.
	rec := postRawJSON(t, r, "/api/v1/lottery/verify-ticket", `{
		"issue": "26102",
		"bets": [
			"01 03 09 11 13 + 06 09",
			{"front": "01,03,07,11,13", "back": "06,09"},
			{"front": [11,13,15,17,19], "back": [9,10]}
		]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var res struct {
		WinningBets int `json:"winning_bets"`
		Bets        []struct {
			Won   bool `json:"won"`
			Prize *struct {
				Tier int `json:"tier"`
			} `json:"prize"`
		} `json:"bets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.WinningBets != 2 {
		t.Fatalf("winning_bets = %d, want 2", res.WinningBets)
	}
	if res.Bets[0].Prize == nil || res.Bets[0].Prize.Tier != 7 {
		t.Errorf("bet #1 expected 七等奖, got %+v", res.Bets[0].Prize)
	}
	if res.Bets[1].Prize == nil || res.Bets[1].Prize.Tier != 6 {
		t.Errorf("bet #2 expected 六等奖, got %+v", res.Bets[1].Prize)
	}
	if res.Bets[2].Won {
		t.Error("bet #3 should not win")
	}
}

func TestVerifyTicketTextInput(t *testing.T) {
	mock := mockWinningAPI(t)
	defer mock.Close()
	r := newLotteryRouter(t, mock.URL)

	// Text body, no explicit issue: the 期 line supplies it.
	rec := postRawJSON(t, r, "/api/v1/lottery/verify-ticket",
		`{"text": "第26102期\n01 03 09 11 13 + 06 09\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Issue       string `json:"issue"`
		WinningBets int    `json:"winning_bets"`
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Issue != "26102" || res.WinningBets != 1 {
		t.Errorf("issue=%q winning_bets=%d, want 26102/1", res.Issue, res.WinningBets)
	}
}

func TestParseTicketErrors(t *testing.T) {
	r := newLotteryRouter(t, "")
	cases := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"short bet", `{"bets":[{"front":[1,2,3],"back":[1,2]}]}`},
		{"out of range", `{"bets":[{"front":[1,2,3,4,99],"back":[1,2]}]}`},
		{"unparseable text", `{"text":"完全没有任何号码的文本"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := postRawJSON(t, r, "/api/v1/lottery/parse-ticket", c.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestWinningEndpoint(t *testing.T) {
	mock := mockWinningAPI(t)
	defer mock.Close()
	r := newLotteryRouter(t, mock.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/lottery/winning/26102", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var w struct {
		Issue string  `json:"issue"`
		Front []int   `json:"front"`
		Back  []int   `json:"back"`
		Pool  float64 `json:"pool_balance"`
		Pool8 bool    `json:"pool_above_8yi"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatal(err)
	}
	if w.Issue != "26102" || len(w.Front) != 5 || len(w.Back) != 2 {
		t.Errorf("unexpected draw: %+v", w)
	}
	if w.Pool8 {
		t.Error("pool should be below 8亿")
	}

	// Unknown issue -> 404.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/lottery/winning/99999", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown issue status = %d, want 404", rec.Code)
	}
}
