package lottery

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Ticket represents one printed 大乐透 ticket, which normally contains several
// bets (单式票常见 5 注). Real ticket QR codes only carry a link, not the
// numbers, so tickets are supplied as structured bet data.
type Ticket struct {
	Issue string       `json:"issue,omitempty" example:"26102"` // 期号（可选）
	Bets  []SuperLotto `json:"bets"`                            // 一注或多注号码
}

// BetResult is the verification outcome for a single bet.
type BetResult struct {
	Index        int    `json:"index"`         // 注序号（从 1 开始）
	Front        []int  `json:"front"`         // 前区号码
	Back         []int  `json:"back"`          // 后区号码
	MatchedFront int    `json:"matched_front"` // 命中前区个数
	MatchedBack  int    `json:"matched_back"`  // 命中后区个数
	Won          bool   `json:"won"`           // 该注是否中奖
	Prize        *Prize `json:"prize,omitempty"`
	Amount       string `json:"amount,omitempty"` // 按奖池档位算出的单注奖金
}

// TicketVerifyResult summarizes the verification of a whole ticket.
type TicketVerifyResult struct {
	Issue       string          `json:"issue,omitempty"`   // 期号
	Winning     *WinningNumbers `json:"winning,omitempty"` // 使用的开奖号码
	TotalBets   int             `json:"total_bets"`        // 总注数
	WinningBets int             `json:"winning_bets"`      // 中奖注数
	TotalWon    bool            `json:"total_won"`         // 是否至少中一注
	Bets        []BetResult     `json:"bets"`              // 逐注结果
}

// NewBet builds a validated single bet from front/back slices.
func NewBet(front, back []int) (*SuperLotto, error) {
	b, err := newSuperLotto(front, back)
	if err != nil {
		return nil, err
	}
	b.Sort()
	return b, nil
}

// AddBet appends a bet parsed from numeric strings, e.g. front ["04","10",...]
// and back ["10","12"]. Empty strings and blanks are ignored.
func (t *Ticket) AddBet(front, back []string) error {
	f, err := parseInts(front)
	if err != nil {
		return fmt.Errorf("front area: %w", err)
	}
	b, err := parseInts(back)
	if err != nil {
		return fmt.Errorf("back area: %w", err)
	}
	bet, err := NewBet(f, b)
	if err != nil {
		return err
	}
	t.Bets = append(t.Bets, *bet)
	return nil
}

// AddBetFromString parses one bet from strings like "04,10,12,16,20" +
// "10,12", or a single 7-number string.
func (t *Ticket) AddBetFromString(front, back string) error {
	if strings.TrimSpace(back) == "" {
		bet, err := ParseSuperLotto(front)
		if err != nil {
			return err
		}
		bet.Sort()
		t.Bets = append(t.Bets, *bet)
		return nil
	}
	return t.AddBet(splitNumbers(front), splitNumbers(back))
}

// Verify checks every bet against the winning numbers.
func (t *Ticket) Verify(winning *WinningNumbers) (*TicketVerifyResult, error) {
	if len(t.Bets) == 0 {
		return nil, fmt.Errorf("ticket has no bets")
	}
	if winning == nil {
		return nil, fmt.Errorf("winning numbers are required")
	}

	res := &TicketVerifyResult{
		Issue:     t.Issue,
		Winning:   winning,
		TotalBets: len(t.Bets),
		Bets:      make([]BetResult, 0, len(t.Bets)),
	}

	for i := range t.Bets {
		bet := t.Bets[i]
		mf := countMatches(bet.Front, winning.Front)
		mb := countMatches(bet.Back, winning.Back)

		br := BetResult{
			Index:        i + 1,
			Front:        bet.Front,
			Back:         bet.Back,
			MatchedFront: mf,
			MatchedBack:  mb,
		}
		if p, ok := prizeByMatch[[2]int{mf, mb}]; ok {
			br.Won = true
			p := p
			br.Prize = &p
			br.Amount = winning.AmountFor(p)
			res.WinningBets++
			res.TotalWon = true
		}
		res.Bets = append(res.Bets, br)
	}
	return res, nil
}

func parseInts(in []string) ([]int, error) {
	out := make([]int, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", s)
		}
		out = append(out, n)
	}
	return out, nil
}

func splitNumbers(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= '0' && r <= '9')
	})
}

// issuePattern matches an issue marker such as "第26102期" or "26102期".
var issuePattern = regexp.MustCompile(`(\d{4,6})\s*期`)

// metaKeywords mark ticket lines that carry metadata rather than bets.
var metaKeywords = []string{"年", "月", "日", "开奖", "合计", "倍", "元", "票", "序列", "序号", "单式", "复式", "胆拖", "追加"}

// ParseTicketText parses ticket text with one bet per line, e.g.
//
//	第26102期
//	04 10 12 16 20 + 10 12
//	02 20 28 33 35 + 01 09
//
// Blank lines and lines starting with '#' are ignored. A line containing an
// issue marker (…期) sets the issue; lines that clearly hold metadata (date,
// amount, serial) are skipped; a line with 1-6 numbers is treated as a
// mistyped bet and reported as an error. Each bet line must contain 7 numbers
// (5 front + 2 back).
func ParseTicketText(text string) (*Ticket, error) {
	t := &Ticket{}
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Issue line, e.g. "第26102期".
		if m := issuePattern.FindStringSubmatch(line); m != nil {
			if t.Issue == "" {
				t.Issue = m[1]
			}
			continue
		}

		// Pure text or metadata (no useful numbers, or many numbers = serial).
		nums, err := ExtractNumbers(line)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行无法解析（%s）：%v", i+1, line, err)
		}
		if len(nums) == 0 || len(nums) > FrontCount+BackCount || isMetaLine(line) || hasASCIILetter(line) {
			continue
		}
		if len(nums) < FrontCount+BackCount {
			return nil, fmt.Errorf("第 %d 行号码不足（%s）：需要 %d 个号码（前区5+后区2），实际 %d 个",
				i+1, line, FrontCount+BackCount, len(nums))
		}

		bet, err := ParseSuperLotto(line)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行号码无效（%s）：%v", i+1, line, err)
		}
		bet.Sort()
		t.Bets = append(t.Bets, *bet)
	}

	if len(t.Bets) == 0 {
		return nil, fmt.Errorf("未从文本中解析出任何号码，请每行填写一注，例如：04 10 12 16 20 + 10 12")
	}
	return t, nil
}

func isMetaLine(line string) bool {
	for _, kw := range metaKeywords {
		if strings.Contains(line, kw) {
			return true
		}
	}
	return false
}

// hasASCIILetter reports whether the line contains a Latin letter. Bet lines
// only contain digits and separators, so this identifies serial/barcode lines
// such as "910330-292261-116430-816911 057908 BA4Wbg".
func hasASCIILetter(line string) bool {
	for _, r := range line {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}
