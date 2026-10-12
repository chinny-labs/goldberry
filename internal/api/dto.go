package api

import (
	"strings"
	"time"

	"github.com/chinny-labs/goldberry/internal/money"
	"github.com/chinny-labs/goldberry/internal/service"
	"github.com/chinny-labs/goldberry/internal/store"
)

// Money is always an integer in minor units (cents for USD); the household's
// currency says what the unit is. Times are RFC3339 in UTC.

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func tsp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := ts(*t)
	return &s
}

// strp turns "" into null.
func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type currencyDTO struct {
	Code     string `json:"code"`
	Exponent int    `json:"exponent"`
	Symbol   string `json:"symbol"`
}

type householdDTO struct {
	Name              string      `json:"name"`
	Currency          currencyDTO `json:"currency"`
	Timezone          string      `json:"timezone"`
	AllowNegative     bool        `json:"allow_negative"`
	RequestExpiryDays int         `json:"request_expiry_days"`
}

func household(h store.Household, c money.Currency) householdDTO {
	return householdDTO{Name: h.Name, Currency: currencyDTO{c.Code, c.Exponent, c.Symbol}, Timezone: h.Timezone,
		AllowNegative: h.AllowNegative, RequestExpiryDays: h.RequestExpiryDays}
}

type meDTO struct {
	User     userDTO  `json:"user"`
	TokenID  string   `json:"token_id"`
	Label    string   `json:"label"`
	Scopes   []string `json:"scopes"`
	LastUsed *string  `json:"last_used_at"`
}

type userDTO struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
}

func user(u store.User) userDTO {
	return userDTO{u.ID, u.Username, u.DisplayName, string(u.Role), u.Disabled()}
}

type kidDTO struct {
	userDTO
	Total     int64 `json:"total"`
	Held      int64 `json:"held"`
	Available int64 `json:"available"`
}

func kidSummary(k service.KidSummary) kidDTO {
	return kidDTO{userDTO: user(k.Kid), Total: k.Total, Held: k.Held, Available: k.Available()}
}

type lockDTO struct {
	ID        string  `json:"id"`
	JarID     string  `json:"jar_id"`
	ToJarID   *string `json:"to_jar_id"`
	SetBy     string  `json:"set_by_role"`
	Hard      bool    `json:"hard"`
	Reason    string  `json:"reason"`
	UntilDate *string `json:"until_date"`
	CreatedAt string  `json:"created_at"`
}

func lock(l store.JarLock) lockDTO {
	return lockDTO{l.ID, l.JarID, strp(l.ToJarID), string(l.SetByRole), l.Hard(), l.Reason, strp(l.UntilDate), ts(l.CreatedAt)}
}

type jarDTO struct {
	ID        string    `json:"id"`
	KidID     string    `json:"kid_id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	SortOrder int       `json:"sort_order"`
	Archived  bool      `json:"archived"`
	Balance   int64     `json:"balance"`
	Held      int64     `json:"held"`
	Available int64     `json:"available"`
	SplitBPS  int       `json:"split_bps"`
	Locks     []lockDTO `json:"locks"`
}

func jar(j service.JarView) jarDTO {
	d := jarDTO{ID: j.ID, KidID: j.KidID, Name: j.Name, Kind: string(j.Kind), SortOrder: j.SortOrder,
		Archived: j.ArchivedAt != nil, Balance: j.Balance, Held: j.Held, Available: j.Available(), SplitBPS: j.BPS,
		Locks: []lockDTO{}}
	for _, l := range j.Locks {
		d.Locks = append(d.Locks, lock(l))
	}
	return d
}

type kidDetailDTO struct {
	kidDTO
	Jars            []jarDTO `json:"jars"`
	PendingRequests int      `json:"pending_requests"`
}

type entryDTO struct {
	ID             string  `json:"id"`
	KidID          string  `json:"kid_id"`
	JarID          string  `json:"jar_id"`
	JarName        string  `json:"jar_name"`
	Amount         int64   `json:"amount"`
	Kind           string  `json:"kind"`
	Comment        string  `json:"comment"`
	PrivateNote    string  `json:"private_note"`
	ActorID        *string `json:"actor_id"`
	ActorName      string  `json:"actor_name"`
	ReversesID     *string `json:"reverses_id"`
	ReversedByID   *string `json:"reversed_by_id"`
	TransferID     *string `json:"transfer_id"`
	BatchID        *string `json:"batch_id"`
	RequestID      *string `json:"request_id"`
	IdempotencyKey *string `json:"idempotency_key"`
	EffectiveAt    string  `json:"effective_at"`
	CreatedAt      string  `json:"created_at"`
}

func entry(e store.LedgerEntry) entryDTO {
	return entryDTO{ID: e.ID, KidID: e.KidID, JarID: e.JarID, JarName: e.JarName, Amount: e.Amount, Kind: string(e.Kind),
		Comment: e.Comment, PrivateNote: e.PrivateNote, ActorID: strp(e.ActorID), ActorName: e.ActorName,
		ReversesID: strp(e.ReversesID), ReversedByID: strp(e.ReversedByID), TransferID: strp(e.TransferID),
		BatchID: strp(e.BatchID), RequestID: strp(e.RequestID), IdempotencyKey: strp(strings.TrimPrefix(e.IdempotencyKey, keyNamespace)),
		EffectiveAt: ts(e.EffectiveAt), CreatedAt: ts(e.CreatedAt)}
}

func entries(es []store.LedgerEntry) []entryDTO {
	out := make([]entryDTO, 0, len(es))
	for _, e := range es {
		out = append(out, entry(e))
	}
	return out
}

type goalDTO struct {
	ID        string  `json:"id"`
	KidID     string  `json:"kid_id"`
	JarID     string  `json:"jar_id"`
	JarName   string  `json:"jar_name"`
	Name      string  `json:"name"`
	Emoji     string  `json:"emoji"`
	Target    int64   `json:"target"`
	Funded    int64   `json:"funded"`
	Remaining int64   `json:"remaining"`
	Percent   int     `json:"percent"`
	Reached   bool    `json:"reached"`
	Priority  int     `json:"priority"`
	CreatedAt string  `json:"created_at"`
	Archived  *string `json:"archived_at"`
}

func goal(g service.GoalView) goalDTO {
	return goalDTO{ID: g.ID, KidID: g.KidID, JarID: g.JarID, JarName: g.JarName, Name: g.Name, Emoji: g.Emoji,
		Target: g.TargetAmount, Funded: g.Funded, Remaining: g.Remaining(), Percent: g.Percent(), Reached: g.Reached(),
		Priority: g.Priority, CreatedAt: ts(g.CreatedAt), Archived: tsp(g.ArchivedAt)}
}

type requestDTO struct {
	ID             string  `json:"id"`
	KidID          string  `json:"kid_id"`
	KidName        string  `json:"kid_name"`
	JarID          string  `json:"jar_id"`
	JarName        string  `json:"jar_name"`
	Amount         int64   `json:"amount"`
	ApprovedAmount int64   `json:"approved_amount"`
	Reason         string  `json:"reason"`
	GoalID         *string `json:"goal_id"`
	Status         string  `json:"status"`
	DecidedByName  string  `json:"decided_by_name"`
	DecisionNote   string  `json:"decision_note"`
	DecidedAt      *string `json:"decided_at"`
	CreatedAt      string  `json:"created_at"`
	ExpiresAt      *string `json:"expires_at"`
}

func request(r store.WithdrawalRequest) requestDTO {
	return requestDTO{ID: r.ID, KidID: r.KidID, KidName: r.KidName, JarID: r.JarID, JarName: r.JarName, Amount: r.Amount,
		ApprovedAmount: r.ApprovedAmount, Reason: r.Reason, GoalID: strp(r.GoalID), Status: string(r.Status),
		DecidedByName: r.DecidedByName, DecisionNote: r.DecisionNote, DecidedAt: tsp(r.DecidedAt),
		CreatedAt: ts(r.CreatedAt), ExpiresAt: tsp(r.ExpiresAt)}
}

func requests(rs []store.WithdrawalRequest) []requestDTO {
	out := make([]requestDTO, 0, len(rs))
	for _, r := range rs {
		out = append(out, request(r))
	}
	return out
}

// list wraps a collection so the response can grow (cursors) without breaking callers.
type list[T any] struct {
	Items []T `json:"items"`
}
