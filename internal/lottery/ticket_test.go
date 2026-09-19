package lottery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestVerifyRealTicketAndIssue26102 checks the exact ticket from the user photo
// against the official draw for issue 26102 (2026-09-07):
// front 01 03 07 27 28, back 06 07. The ticket does not win.
func TestParseTicketText(t *testing.T) {
	// A realistic paste of a whole ticket, including metadata lines.
	text := `体彩 超级大乐透
第 26102期        2026年09月07日开奖
910330-292261-116430-816911 057908 BA4Wbg
单式票        1倍        合计10元
① 04 10 12 16 20 + 10 12
② 02 20 28 33 35 + 01 09
③ 11 13 20 25 34 + 03 12
④ 09 11 15 28 29 + 07 12
⑤ 02 18 19 21 31 + 05 06`

	tk, err := ParseTicketText(text)
	if err != nil {
		t.Fatalf("ParseTicketText: %v", err)
	}
	if len(tk.Bets) != 5 {
		t.Fatalf("got %d bets, want 5", len(tk.Bets))
	}
	if tk.Issue != "26102" {
		t.Errorf("issue = %q, want 26102", tk.Issue)
	}
	want := [][2][]int{
		{{4, 10, 12, 16, 20}, {10, 12}},
		{{2, 20, 28, 33, 35}, {1, 9}},
		{{11, 13, 20, 25, 34}, {3, 12}},
		{{9, 11, 15, 28, 29}, {7, 12}},
		{{2, 18, 19, 21, 31}, {5, 6}},
	}
	for i, w := range want {
		if !equalInts(tk.Bets[i].Front, w[0]) || !equalInts(tk.Bets[i].Back, w[1]) {
			t.Errorf("bet #%d = %v + %v, want %v + %v", i+1,
				tk.Bets[i].Front, tk.Bets[i].Back, w[0], w[1])
		}
	}
}

func TestParseTicketTextMinimal(t *testing.T) {
	// Plain comma-separated lines and comment lines.
	tk, err := ParseTicketText("# 我的票\n04,10,12,16,20,10,12\n\n02 20 28 33 35 01 09\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(tk.Bets) != 2 {
		t.Fatalf("got %d bets, want 2", len(tk.Bets))
	}
	if !equalInts(tk.Bets[0].Front, []int{4, 10, 12, 16, 20}) {
		t.Errorf("bet #1 front = %v", tk.Bets[0].Front)
	}
}

func TestParseTicketTextErrors(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"only metadata", "体彩 大乐透\n2026年09月07日开奖\n单式票 1倍 合计10元"},
		{"short bet line", "04 10 12 16 20\n"},           // 5 numbers -> typo
		{"duplicate number", "04 04 12 16 20 + 10 12\n"}, // duplicate front
		{"out of range", "04 10 12 16 99 + 10 12\n"},     // front 99 invalid
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseTicketText(c.text); err == nil {
				t.Errorf("ParseTicketText(%q) expected error", c.text)
			}
		})
	}
}

func TestAddBetFromString(t *testing.T) {
	tk := &Ticket{}
	if err := tk.AddBetFromString("04,10,12,16,20", "10,12"); err != nil {
		t.Fatal(err)
	}
	if err := tk.AddBetFromString("02 20 28 33 35 + 01 09", ""); err != nil {
		t.Fatal(err)
	}
	if len(tk.Bets) != 2 {
		t.Fatalf("got %d bets, want 2", len(tk.Bets))
	}
	if !equalInts(tk.Bets[1].Back, []int{1, 9}) {
		t.Errorf("bet #2 back = %v", tk.Bets[1].Back)
	}
}

func TestVerifyTicketRealTicketAndIssue26102(t *testing.T) {
	ticket := &Ticket{Issue: "26102"}
	bets := []struct {
		front []int
		back  []int
		wantF int
		wantB int
	}{
		{[]int{4, 10, 12, 16, 20}, []int{10, 12}, 0, 0}, // ①
		{[]int{2, 20, 28, 33, 35}, []int{1, 9}, 1, 0},   // ②
		{[]int{11, 13, 20, 25, 34}, []int{3, 12}, 0, 0}, // ③
		{[]int{9, 11, 15, 28, 29}, []int{7, 12}, 1, 1},  // ④
		{[]int{2, 18, 19, 21, 31}, []int{5, 6}, 0, 1},   // ⑤
	}
	for i, b := range bets {
		bet, err := NewBet(b.front, b.back)
		if err != nil {
			t.Fatalf("bet #%d: %v", i+1, err)
		}
		ticket.Bets = append(ticket.Bets, *bet)
	}

	winning, err := ParseWinningNumbers("01,03,07,27,28", "06,07")
	if err != nil {
		t.Fatal(err)
	}

	res, err := ticket.Verify(winning)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalBets != 5 {
		t.Errorf("total_bets = %d, want 5", res.TotalBets)
	}
	if res.WinningBets != 0 || res.TotalWon {
		t.Errorf("expected no winning bets, got winning_bets=%d total_won=%v", res.WinningBets, res.TotalWon)
	}
	for i, br := range res.Bets {
		if br.MatchedFront != bets[i].wantF || br.MatchedBack != bets[i].wantB {
			t.Errorf("bet #%d matched %d+%d, want %d+%d",
				i+1, br.MatchedFront, br.MatchedBack, bets[i].wantF, bets[i].wantB)
		}
		if br.Won {
			t.Errorf("bet #%d should not win", i+1)
		}
	}
}

func TestVerifyTicketWithWinner(t *testing.T) {
	ticket := &Ticket{Issue: "26102"}
	// Bet 1 wins 七等奖 (2+1); bet 2 wins 六等奖 (3+1).
	add := func(front, back []int) {
		bet, err := NewBet(front, back)
		if err != nil {
			t.Fatal(err)
		}
		ticket.Bets = append(ticket.Bets, *bet)
	}
	add([]int{1, 3, 9, 11, 13}, []int{6, 9})     // vs 01 03 07 27 28 / 06 07 -> 2+1
	add([]int{1, 3, 7, 11, 13}, []int{6, 9})     // -> 3+1
	add([]int{11, 13, 15, 17, 19}, []int{9, 10}) // -> 0+0

	winning, _ := ParseWinningNumbers("01,03,07,27,28", "06,07")
	res, err := ticket.Verify(winning)
	if err != nil {
		t.Fatal(err)
	}
	if res.WinningBets != 2 || !res.TotalWon {
		t.Fatalf("winning_bets = %d, total_won = %v; want 2, true", res.WinningBets, res.TotalWon)
	}
	if res.Bets[0].Prize.Tier != 7 {
		t.Errorf("bet #1 tier = %d, want 7", res.Bets[0].Prize.Tier)
	}
	if res.Bets[1].Prize.Tier != 6 {
		t.Errorf("bet #2 tier = %d, want 6", res.Bets[1].Prize.Tier)
	}
	if res.Bets[2].Won {
		t.Error("bet #3 should not win")
	}
	// Pool below 8亿 -> 第六等奖 = 15元, 第七等奖 = 5元.
	if res.Bets[0].Amount != "5元" {
		t.Errorf("bet #1 amount = %q, want 5元", res.Bets[0].Amount)
	}
	if res.Bets[1].Amount != "15元" {
		t.Errorf("bet #2 amount = %q, want 15元", res.Bets[1].Amount)
	}
}

func TestNewBetValidation(t *testing.T) {
	if _, err := NewBet([]int{1, 2, 3}, []int{1, 2}); err == nil {
		t.Error("expected error for short front area")
	}
	if _, err := NewBet([]int{1, 2, 3, 4, 36}, []int{1, 2}); err == nil {
		t.Error("expected error for out-of-range front number")
	}
	if _, err := NewBet([]int{1, 2, 3, 4, 5}, []int{1, 13}); err == nil {
		t.Error("expected error for out-of-range back number")
	}
	if _, err := NewBet([]int{1, 2, 3, 4, 5}, []int{1, 1}); err == nil {
		t.Error("expected error for duplicate back number")
	}
}

func TestTicketVerifyNoBets(t *testing.T) {
	tk := &Ticket{}
	w, _ := ParseWinningNumbers("01,03,07,27,28", "06,07")
	if _, err := tk.Verify(w); err == nil {
		t.Error("expected error when ticket has no bets")
	}
}

func TestPoolAbove8SelectsHigherAmount(t *testing.T) {
	ticket := &Ticket{}
	bet, _ := NewBet([]int{1, 3, 9, 11, 13}, []int{6, 9}) // 2+1 -> 七等奖
	ticket.Bets = append(ticket.Bets, *bet)

	w, _ := ParseWinningNumbers("01,03,07,27,28", "06,07")
	w.PoolAbove8 = true
	res, _ := ticket.Verify(w)
	if res.Bets[0].Amount != "7元" {
		t.Errorf("amount = %q, want 7元 for pool >= 8亿", res.Bets[0].Amount)
	}
}

// TestWinningFetcherWithMockServer verifies parsing of the official API shape.
func TestWinningFetcherWithMockServer(t *testing.T) {
	payload := map[string]any{
		"success": true,
		"value": map[string]any{
			"list": []map[string]any{
				{
					"lotteryDrawNum":       "26106",
					"lotteryDrawTime":      "2026-09-16",
					"lotteryDrawResult":    "14 17 21 25 28 04 06",
					"poolBalanceAfterdraw": "826,970,066.74",
				},
				{
					"lotteryDrawNum":       "26102",
					"lotteryDrawTime":      "2026-09-07",
					"lotteryDrawResult":    "01 03 07 27 28 06 07",
					"poolBalanceAfterdraw": "725,321,799.11",
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	f := NewWinningFetcher(srv.URL)
	w, err := f.Fetch(context.Background(), "26102")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Front) != 5 || w.Front[0] != 1 || w.Front[4] != 28 {
		t.Errorf("front = %v, want [1 3 7 27 28]", w.Front)
	}
	if len(w.Back) != 2 || w.Back[0] != 6 || w.Back[1] != 7 {
		t.Errorf("back = %v, want [6 7]", w.Back)
	}
	if w.PoolAbove8 {
		t.Error("pool 725,321,799.11 is below 8亿, want PoolAbove8=false")
	}
	if w.DrawTime != "2026-09-07" {
		t.Errorf("draw_time = %q", w.DrawTime)
	}

	// Second call must hit the cache (server can be closed).
	srv.Close()
	if _, err := f.Fetch(context.Background(), "26102"); err != nil {
		t.Errorf("cached fetch failed: %v", err)
	}
}

func TestWinningFetcherIssueNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "value": map[string]any{"list": []any{}}})
	}))
	defer srv.Close()

	f := NewWinningFetcher(srv.URL)
	if _, err := f.Fetch(context.Background(), "99999"); err == nil {
		t.Fatal("expected error for missing issue")
	} else if !errors.Is(err, ErrIssueNotFound) {
		t.Errorf("error = %v, want ErrIssueNotFound", err)
	}
}
