package lottery

import (
	"testing"
)

func mustParse(t *testing.T, s string) *SuperLotto {
	t.Helper()
	tk, err := ParseSuperLotto(s)
	if err != nil {
		t.Fatalf("ParseSuperLotto(%q) failed: %v", s, err)
	}
	return tk
}

func TestParseSuperLotto(t *testing.T) {
	cases := []struct {
		in         string
		wantFront  []int
		wantBack   []int
		shouldFail bool
	}{
		{"05,12,18,23,35,01,08", []int{5, 12, 18, 23, 35}, []int{1, 8}, false},
		{"05 12 18 23 35 01 08", []int{5, 12, 18, 23, 35}, []int{1, 8}, false},
		{"05 12 18 23 35 + 01 08", []int{5, 12, 18, 23, 35}, []int{1, 8}, false},
		{"5-12-18-23-35-1-8", []int{5, 12, 18, 23, 35}, []int{1, 8}, false},
		{"前区:05,12,18,23,35 后区:01,08", []int{5, 12, 18, 23, 35}, []int{1, 8}, false},
		{"05,12,18,23,35,01", nil, nil, true},       // only 6 numbers
		{"05,05,18,23,35,01,08", nil, nil, true},    // duplicate front
		{"05,12,18,23,36,01,08", nil, nil, true},    // front 36 out of range
		{"05,12,18,23,35,01,13", nil, nil, true},    // back 13 out of range
		{"hello world", nil, nil, true},             // no numbers
		{"05,12,18,23,35,01,08,09", nil, nil, true}, // too many (9 numbers)
	}
	for _, c := range cases {
		tk, err := ParseSuperLotto(c.in)
		if c.shouldFail {
			if err == nil {
				t.Errorf("ParseSuperLotto(%q) expected error, got %+v", c.in, tk)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSuperLotto(%q) unexpected error: %v", c.in, err)
			continue
		}
		if !equalInts(tk.Front, c.wantFront) {
			t.Errorf("ParseSuperLotto(%q) front = %v, want %v", c.in, tk.Front, c.wantFront)
		}
		if !equalInts(tk.Back, c.wantBack) {
			t.Errorf("ParseSuperLotto(%q) back = %v, want %v", c.in, tk.Back, c.wantBack)
		}
	}
}

func TestVerifyPrizeTiers(t *testing.T) {
	// (ticket, winningFront, winningBack, expected tier or 0 for no prize)
	cases := []struct {
		name       string
		ticket     string
		winFront   string
		winBack    string
		wantTier   int
		wantWon    bool
		wantMatchF int
		wantMatchB int
	}{
		{"一等奖 5+2", "05,12,18,23,35,01,08", "05,12,18,23,35", "01,08", 1, true, 5, 2},
		{"二等奖 5+1", "05,12,18,23,35,01,08", "05,12,18,23,35", "01,09", 2, true, 5, 1},
		{"三等奖 5+0", "05,12,18,23,35,01,08", "05,12,18,23,35", "09,10", 3, true, 5, 0},
		{"三等奖 4+2", "05,12,18,23,35,01,08", "05,12,18,23,34", "01,08", 3, true, 4, 2},
		{"四等奖 4+1", "05,12,18,23,35,01,08", "05,12,18,23,34", "01,09", 4, true, 4, 1},
		{"五等奖 3+2", "05,12,18,23,35,01,08", "05,12,18,30,34", "01,08", 5, true, 3, 2},
		{"五等奖 4+0", "05,12,18,23,35,01,08", "05,12,18,23,34", "09,10", 5, true, 4, 0},
		{"六等奖 3+1", "05,12,18,23,35,01,08", "05,12,18,30,34", "01,09", 6, true, 3, 1},
		{"六等奖 2+2", "05,12,18,23,35,01,08", "05,12,20,30,34", "01,08", 6, true, 2, 2},
		{"七等奖 3+0", "05,12,18,23,35,01,08", "05,12,18,30,34", "09,10", 7, true, 3, 0},
		{"七等奖 1+2", "05,12,18,23,35,01,08", "05,20,30,32,34", "01,08", 7, true, 1, 2},
		{"七等奖 2+1", "05,12,18,23,35,01,08", "05,12,20,30,34", "01,09", 7, true, 2, 1},
		{"七等奖 0+2", "05,12,18,23,35,01,08", "10,20,30,32,34", "01,08", 7, true, 0, 2},
		{"未中奖 2+0", "05,12,18,23,35,01,08", "05,12,20,30,34", "09,10", 0, false, 2, 0},
		{"未中奖 1+1", "05,12,18,23,35,01,08", "05,20,30,32,34", "01,09", 0, false, 1, 1},
		{"未中奖 0+1", "05,12,18,23,35,01,08", "10,20,30,32,34", "01,09", 0, false, 0, 1},
		{"未中奖 0+0", "05,12,18,23,35,01,08", "10,20,30,32,34", "09,10", 0, false, 0, 0},
		{"未中奖 4+2异号", "05,12,18,23,35,01,08", "05,12,18,23,35", "09,10", 3, true, 5, 0}, // sanity: 5+0 → 三等奖
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tk := mustParse(t, c.ticket)
			res, err := tk.Verify(c.winFront, c.winBack)
			if err != nil {
				t.Fatalf("Verify failed: %v", err)
			}
			if res.MatchedFront != c.wantMatchF || res.MatchedBack != c.wantMatchB {
				t.Errorf("matched = %d+%d, want %d+%d", res.MatchedFront, res.MatchedBack, c.wantMatchF, c.wantMatchB)
			}
			if res.Won != c.wantWon {
				t.Errorf("won = %v, want %v", res.Won, c.wantWon)
			}
			if c.wantTier == 0 {
				if res.Prize != nil {
					t.Errorf("expected no prize, got %+v", res.Prize)
				}
			} else {
				if res.Prize == nil {
					t.Fatalf("expected prize tier %d, got nil", c.wantTier)
				}
				if res.Prize.Tier != c.wantTier {
					t.Errorf("prize tier = %d, want %d", res.Prize.Tier, c.wantTier)
				}
				if res.Prize.TierName == "" || res.Prize.Condition == "" {
					t.Errorf("prize missing name/condition: %+v", res.Prize)
				}
			}
		})
	}
}

func TestVerifyInvalidWinningNumbers(t *testing.T) {
	tk := mustParse(t, "05,12,18,23,35,01,08")
	if _, err := tk.Verify("05,12,18,23", "01,08"); err == nil {
		t.Error("expected error for short winning front area")
	}
	if _, err := tk.Verify("05,12,18,23,34", "01,13"); err == nil {
		t.Error("expected error for back number out of range")
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
