package lottery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrIssueNotFound means the requested issue is not in the draw history.
var ErrIssueNotFound = errors.New("issue not found")

// DefaultWinningEndpoint is the official China Sports Lottery history API for
// 超级大乐透 (gameNo=85).
const DefaultWinningEndpoint = "https://webapi.sporttery.cn/gateway/lottery/getHistoryPageListV1.qry"

// PoolThreshold8Yi is the 8亿 pool threshold that selects the higher prize column.
const PoolThreshold8Yi = 800_000_000.0

// WinningNumbers is the official draw result for one 大乐透 issue.
type WinningNumbers struct {
	Issue       string  `json:"issue" example:"26102"`                    // 期号
	DrawTime    string  `json:"draw_time,omitempty" example:"2026-09-07"` // 开奖日期
	Front       []int   `json:"front" example:"1,3,7,27,28"`              // 前区开奖号码
	Back        []int   `json:"back" example:"6,7"`                       // 后区开奖号码
	PoolBalance float64 `json:"pool_balance,omitempty"`                   // 开奖后奖池（元）
	PoolAbove8  bool    `json:"pool_above_8yi"`                           // 奖池是否达到 8 亿
}

// AmountFor selects the single-bet amount for a prize given the pool state.
func (w *WinningNumbers) AmountFor(p Prize) string {
	if w != nil && w.PoolAbove8 {
		return p.AmountAbove8
	}
	return p.AmountBelow8
}

// ParseWinningNumbers builds WinningNumbers from caller-supplied front/back
// strings, e.g. front "01,03,07,27,28" and back "06,07".
func ParseWinningNumbers(front, back string) (*WinningNumbers, error) {
	tk, err := ParseSuperLotto(front + "," + back)
	if err != nil {
		return nil, fmt.Errorf("invalid winning numbers: %w", err)
	}
	tk.Sort()
	return &WinningNumbers{Front: tk.Front, Back: tk.Back}, nil
}

// prizeLevel mirrors one entry of the API's prizeLevelList.
type prizeLevel struct {
	PrizeLevel  string `json:"prizeLevel"`
	StakeAmount string `json:"stakeAmount"`
	StakeCount  string `json:"stakeCount"`
}

type drawItem struct {
	DrawNum     string       `json:"lotteryDrawNum"`
	DrawTime    string       `json:"lotteryDrawTime"`
	DrawResult  string       `json:"lotteryDrawResult"`
	PoolBalance string       `json:"poolBalanceAfterdraw"`
	PrizeLevels []prizeLevel `json:"prizeLevelList"`
}

type historyResponse struct {
	Success bool `json:"success"`
	Value   struct {
		List []drawItem `json:"list"`
	} `json:"value"`
}

// WinningFetcher fetches and caches official 大乐透 draw results.
type WinningFetcher struct {
	endpoint string
	http     *http.Client

	mu    sync.Mutex
	cache map[string]*WinningNumbers
}

// NewWinningFetcher creates a fetcher. An empty endpoint uses the official API.
func NewWinningFetcher(endpoint string) *WinningFetcher {
	if endpoint == "" {
		endpoint = DefaultWinningEndpoint
	}
	return &WinningFetcher{
		endpoint: endpoint,
		http:     &http.Client{Timeout: 15 * time.Second},
		cache:    make(map[string]*WinningNumbers),
	}
}

// Fetch returns the draw result for the given issue (e.g. "26102").
// Results are cached in memory for the lifetime of the process.
func (f *WinningFetcher) Fetch(ctx context.Context, issue string) (*WinningNumbers, error) {
	issue = strings.TrimSpace(issue)
	if issue == "" {
		return nil, fmt.Errorf("issue is required")
	}

	f.mu.Lock()
	if w, ok := f.cache[issue]; ok {
		f.mu.Unlock()
		return w, nil
	}
	f.mu.Unlock()

	// Page through recent draws (newest first) until the issue is found.
	const pageSize = 100
	for page := 1; page <= 5; page++ {
		url := fmt.Sprintf("%s?gameNo=85&provinceId=0&pageSize=%d&isVerify=1&pageNo=%d",
			f.endpoint, pageSize, page)
		items, err := f.fetchPage(ctx, url)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			break
		}
		for i := range items {
			if items[i].DrawNum != issue {
				continue
			}
			w, err := parseDraw(&items[i])
			if err != nil {
				return nil, err
			}
			f.mu.Lock()
			f.cache[issue] = w
			f.mu.Unlock()
			return w, nil
		}
		// Older issues are on later pages; stop if the page is not full.
		if len(items) < pageSize {
			break
		}
	}
	return nil, fmt.Errorf("%w: %s not in recent draw history", ErrIssueNotFound, issue)
}

func (f *WinningFetcher) fetchPage(ctx context.Context, url string) ([]drawItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://www.sporttery.cn/")

	resp, err := f.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call lottery API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lottery API returned status %d", resp.StatusCode)
	}

	var hr historyResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return nil, fmt.Errorf("decode lottery API response: %w", err)
	}
	if !hr.Success {
		return nil, fmt.Errorf("lottery API reported failure")
	}
	return hr.Value.List, nil
}

func parseDraw(it *drawItem) (*WinningNumbers, error) {
	nums, err := extractNumbers(it.DrawResult)
	if err != nil {
		return nil, fmt.Errorf("parse draw result %q: %w", it.DrawResult, err)
	}
	if len(nums) < FrontCount+BackCount {
		return nil, fmt.Errorf("draw result %q has too few numbers", it.DrawResult)
	}

	pool := parseAmount(it.PoolBalance)
	return &WinningNumbers{
		Issue:       it.DrawNum,
		DrawTime:    it.DrawTime,
		Front:       nums[:FrontCount],
		Back:        nums[FrontCount : FrontCount+BackCount],
		PoolBalance: pool,
		PoolAbove8:  pool >= PoolThreshold8Yi,
	}, nil
}

// parseAmount turns "725,321,799.11" into 725321799.11.
func parseAmount(s string) float64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	if s == "" || s == "---" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
