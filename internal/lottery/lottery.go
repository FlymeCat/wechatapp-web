// Package lottery implements 大乐透 (Chinese Super Lotto) number parsing,
// QR-code recognition and prize verification.
package lottery

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SuperLotto constants.
const (
	FrontCount = 5  // 前区选 5 个号码
	BackCount  = 2  // 后区选 2 个号码
	FrontMax   = 35 // 前区号码范围 1-35
	BackMax    = 12 // 后区号码范围 1-12
)

// SuperLotto represents a 大乐透 ticket: 5 front numbers (1-35) + 2 back numbers (1-12).
type SuperLotto struct {
	Front []int `json:"front" example:"5,12,18,23,35"` // 前区号码
	Back  []int `json:"back"  example:"1,8"`           // 后区号码
}

// Prize is a single prize tier of 大乐透.
type Prize struct {
	Tier         int    `json:"tier"`             // 奖级（一等奖=1 … 七等奖=7）
	TierName     string `json:"tier_name"`        // 奖级名称，如"一等奖"
	Condition    string `json:"condition"`        // 中奖条件，如"5+2"
	AmountBelow8 string `json:"amount_below_8yi"` // 奖池 8 亿以下单注奖金
	AmountAbove8 string `json:"amount_above_8yi"` // 奖池 8 亿以上单注奖金
}

// VerifyResult is the outcome of comparing a ticket against the winning numbers.
type VerifyResult struct {
	MatchedFront int    `json:"matched_front"`   // 命中前区个数
	MatchedBack  int    `json:"matched_back"`    // 命中后区个数
	Won          bool   `json:"won"`             // 是否中奖
	Prize        *Prize `json:"prize,omitempty"` // 中奖信息（未中奖时为 null）
}

// prizeByMatch maps (matchedFront, matchedBack) to the prize tier.
// Source: 中国体育彩票 大乐透奖级表（中彩网 2026-01-31 版）。
var prizeByMatch = map[[2]int]Prize{
	{5, 2}: {1, "一等奖", "5+2", "浮动（基本投注最高1000万）", "浮动（追加投注最高1800万）"},
	{5, 1}: {2, "二等奖", "5+1", "浮动（追加投注多80%）", "浮动（追加投注多80%）"},
	{5, 0}: {3, "三等奖", "5+0", "5,000元", "6,666元"},
	{4, 2}: {3, "三等奖", "4+2", "5,000元", "6,666元"},
	{4, 1}: {4, "四等奖", "4+1", "300元", "380元"},
	{3, 2}: {5, "五等奖", "3+2", "150元", "200元"},
	{4, 0}: {5, "五等奖", "4+0", "150元", "200元"},
	{3, 1}: {6, "六等奖", "3+1", "15元", "18元"},
	{2, 2}: {6, "六等奖", "2+2", "15元", "18元"},
	{3, 0}: {7, "七等奖", "3+0", "5元", "7元"},
	{1, 2}: {7, "七等奖", "1+2", "5元", "7元"},
	{2, 1}: {7, "七等奖", "2+1", "5元", "7元"},
	{0, 2}: {7, "七等奖", "0+2", "5元", "7元"},
}

// ParseSuperLotto parses a numeric string like "05,12,18,23,35,01,08" or
// "05 12 18 23 35 + 01 08" into a SuperLotto ticket. The first 5 numbers are
// the front area, the last 2 numbers are the back area. The input must
// contain exactly 7 numbers.
func ParseSuperLotto(s string) (*SuperLotto, error) {
	nums, err := extractNumbers(s)
	if err != nil {
		return nil, err
	}
	if len(nums) != FrontCount+BackCount {
		return nil, fmt.Errorf("need exactly %d numbers (5 front + 2 back), got %d: %q", FrontCount+BackCount, len(nums), s)
	}
	return newSuperLotto(nums[:FrontCount], nums[FrontCount:FrontCount+BackCount])
}

// newSuperLotto validates and builds a SuperLotto ticket.
func newSuperLotto(front, back []int) (*SuperLotto, error) {
	if len(front) != FrontCount {
		return nil, fmt.Errorf("front area must have %d numbers, got %d", FrontCount, len(front))
	}
	if len(back) != BackCount {
		return nil, fmt.Errorf("back area must have %d numbers, got %d", BackCount, len(back))
	}
	if err := validateArea("front", front, FrontMax); err != nil {
		return nil, err
	}
	if err := validateArea("back", back, BackMax); err != nil {
		return nil, err
	}
	return &SuperLotto{Front: front, Back: back}, nil
}

func validateArea(name string, nums []int, max int) error {
	seen := make(map[int]bool, len(nums))
	for _, n := range nums {
		if n < 1 || n > max {
			return fmt.Errorf("%s number %d out of range 1-%d", name, n, max)
		}
		if seen[n] {
			return fmt.Errorf("duplicate %s number %d", name, n)
		}
		seen[n] = true
	}
	return nil
}

// ExtractNumbers pulls every integer out of a string, accepting spaces,
// commas, plus signs, dashes and Chinese characters as separators. It is
// exported so API clients can submit numbers as free-form text.
func ExtractNumbers(s string) ([]int, error) {
	return extractNumbers(s)
}

// extractNumbers pulls every integer out of a string, accepting spaces,
// commas, plus signs, dashes and Chinese characters as separators.
func extractNumbers(s string) ([]int, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= '0' && r <= '9')
	})
	nums := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", f)
		}
		nums = append(nums, n)
	}
	return nums, nil
}

// Verify compares a ticket against the winning numbers and returns the result.
// Winning numbers are given as two strings, e.g. front "05,12,18,23,35" and
// back "01,08".
func (t *SuperLotto) Verify(winningFront, winningBack string) (*VerifyResult, error) {
	win, err := ParseSuperLotto(winningFront + "," + winningBack)
	if err != nil {
		return nil, fmt.Errorf("invalid winning numbers: %w", err)
	}

	matchedFront := countMatches(t.Front, win.Front)
	matchedBack := countMatches(t.Back, win.Back)

	res := &VerifyResult{
		MatchedFront: matchedFront,
		MatchedBack:  matchedBack,
	}
	if p, ok := prizeByMatch[[2]int{matchedFront, matchedBack}]; ok {
		res.Won = true
		p := p
		res.Prize = &p
	}
	return res, nil
}

func countMatches(a, b []int) int {
	// Both slices are small (5 and 2); a simple O(n*m) scan is fine and
	// avoids allocating maps.
	count := 0
	for _, x := range a {
		for _, y := range b {
			if x == y {
				count++
				break
			}
		}
	}
	return count
}

// SortNormalizes sorts front/back numbers ascending (for display/compare).
func (t *SuperLotto) Sort() {
	sort.Ints(t.Front)
	sort.Ints(t.Back)
}
