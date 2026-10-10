package store

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Household is the single install-wide row (one install = one family).
type Household struct {
	ID                string
	Name              string
	Currency          string
	Timezone          string
	PINLength         int
	AllowNegative     bool
	RequestExpiryDays int // 0 = requests never expire
	KidPickerLogin    bool
	CreatedAt         time.Time
}

// Location returns the household's time zone, falling back to UTC.
func (h Household) Location() *time.Location {
	if loc, err := time.LoadLocation(h.Timezone); err == nil {
		return loc
	}
	return time.UTC
}

type Role string

const (
	RoleAdmin Role = "admin"
	RoleKid   Role = "kid"
)

type User struct {
	ID           string
	Role         Role
	Username     string
	DisplayName  string
	Avatar       string
	PasswordHash string // admins only
	PINHash      string // kids only
	Email        string // admins only, optional
	DisabledAt   *time.Time
	CreatedAt    time.Time
}

func (u User) IsAdmin() bool  { return u.Role == RoleAdmin }
func (u User) IsKid() bool    { return u.Role == RoleKid }
func (u User) Disabled() bool { return u.DisabledAt != nil }

// Initial is the first letter of the display name, for avatar tiles.
func (u User) Initial() string {
	for _, r := range u.DisplayName {
		return string(r)
	}
	return "?"
}

type Session struct {
	IDHash      string
	UserID      string
	DeviceLabel string
	Remember    bool
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
	RevokedAt   *time.Time
}

// AuthAttempts tracks failed sign-ins per account (plan §4.1).
type AuthAttempts struct {
	UserID        string
	Failures      int
	WindowStart   *time.Time
	NextAllowedAt *time.Time
	LockedAt      *time.Time
}

type JarKind string

const (
	JarSpend  JarKind = "spend"
	JarSave   JarKind = "save"
	JarGive   JarKind = "give"
	JarInvest JarKind = "invest"
	JarCustom JarKind = "custom"
)

type Jar struct {
	ID         string
	KidID      string
	Name       string
	Kind       JarKind
	SortOrder  int
	ArchivedAt *time.Time
}

// JarBalance is a jar with its computed balance and pending holds.
type JarBalance struct {
	Jar
	Balance int64
	Held    int64
}

// Available is what can still be requested or debited: balance − holds.
func (b JarBalance) Available() int64 { return b.Balance - b.Held }

type SplitRule struct {
	KidID       string
	JarID       string
	BasisPoints int
}

type EntryKind string

const (
	KindDeposit     EntryKind = "deposit"
	KindWithdrawal  EntryKind = "withdrawal"
	KindAdjustment  EntryKind = "adjustment"
	KindAllowance   EntryKind = "allowance"
	KindInterest    EntryKind = "interest"
	KindTransferIn  EntryKind = "transfer_in"
	KindTransferOut EntryKind = "transfer_out"
	KindReversal    EntryKind = "reversal"
	KindInvestBuy   EntryKind = "invest_buy"  // cash out of the Invest jar into a buy order
	KindInvestSell  EntryKind = "invest_sell" // a sale's proceeds, up to what was paid
	KindInvestGain  EntryKind = "invest_gain" // a sale's profit, paid by a parent
)

// LedgerEntry is one append-only money movement. Amount is signed minor units.
type LedgerEntry struct {
	ID             string
	KidID          string
	JarID          string
	Amount         int64
	Kind           EntryKind
	Comment        string
	PrivateNote    string
	ActorID        string // "" = system
	ReversesID     string
	TransferID     string
	BatchID        string
	RequestID      string
	ScheduleID     string
	IdempotencyKey string
	EffectiveAt    time.Time
	CreatedAt      time.Time

	// Filled by list queries.
	ActorName    string
	JarName      string
	ReversedByID string // id of the reversal that cancels this entry, if any
}

func (e LedgerEntry) Reversed() bool { return e.ReversedByID != "" }

// GroupKey names the ledger row an entry shows in: the legs of a move share
// their transfer_id, the parts of a split deposit their batch_id.
func (e LedgerEntry) GroupKey() string {
	switch {
	case e.TransferID != "":
		return e.TransferID
	case e.BatchID != "":
		return e.BatchID
	}
	return e.ID
}

type RequestStatus string

const (
	StatusPending   RequestStatus = "pending"
	StatusApproved  RequestStatus = "approved"
	StatusDenied    RequestStatus = "denied"
	StatusCancelled RequestStatus = "cancelled"
	StatusExpired   RequestStatus = "expired"
)

type WithdrawalRequest struct {
	ID             string
	KidID          string
	JarID          string
	Amount         int64
	ApprovedAmount int64 // set when approved; may be lower than Amount
	Reason         string
	GoalID         string
	Status         RequestStatus
	DecidedBy      string
	DecisionNote   string
	DecidedAt      *time.Time
	IdempotencyKey string
	CreatedAt      time.Time
	ExpiresAt      *time.Time // nil = never

	// Filled by list queries.
	KidName       string
	JarName       string
	DecidedByName string
}

type Notification struct {
	ID          string
	RecipientID string
	Kind        string
	Payload     json.RawMessage
	RequestID   string
	ReadAt      *time.Time
	CreatedAt   time.Time
}

type AuditEntry struct {
	ID        string
	ActorID   string
	Action    string
	TargetID  string
	Detail    json.RawMessage
	CreatedAt time.Time
}

// LedgerFilter narrows ListLedger. KidID is required. From and To bound
// effective_at to [From, To); a zero time leaves that side open.
type LedgerFilter struct {
	KidID string
	JarID string
	From  time.Time
	To    time.Time
	Limit int
}

// RequestFilter narrows ListRequests. Empty fields match everything.
type RequestFilter struct {
	KidID  string
	Status RequestStatus
	Limit  int
}

// NewID returns a UUIDv7 string: time-sortable and dialect-independent.
func NewID() string { return uuid.Must(uuid.NewV7()).String() }

// Schedule is a recurring allowance (plan §7.2). Calendar dates are
// "YYYY-MM-DD" in the household time zone.
type Schedule struct {
	ID              string
	KidID           string
	Amount          int64
	Cadence         string // weekly | biweekly | monthly
	Weekday         int    // weekly: 0 = Sunday … 6 = Saturday
	DayOfMonth      int    // monthly: 1–31
	AnchorDate      string // biweekly
	TargetJarID     string // "" = use the kid's split rule
	CommentTemplate string
	Active          bool
	LastOccurrence  string // newest day already paid (or the creation/resume day)
	CreatedBy       string
	CreatedAt       time.Time
}

// JarLock restricts money leaving a jar (plan §5.4). An admin lock is hard;
// a kid's self-lock is soft and can be overridden after the gauntlet.
type JarLock struct {
	ID        string
	JarID     string
	ToJarID   string // "" = every route out and withdrawal requests
	SetByRole Role
	SetBy     string
	Reason    string
	UntilDate string // "" = until removed; lifts at the start of this day
	CreatedAt time.Time
	RemovedBy string
	RemovedAt *time.Time

	// Filled by list queries.
	KidID     string
	JarName   string
	ToJarName string
	SetByName string
}

// Hard reports whether the kid can't get past this lock.
func (l JarLock) Hard() bool { return l.SetByRole == RoleAdmin }

// LockOverride records a kid breaking their own lock.
type LockOverride struct {
	ID            string
	LockID        string
	KidID         string
	Action        string // once | removed
	LedgerEntryID string
	RequestID     string
	CreatedAt     time.Time

	// Filled by list queries.
	Reason  string
	JarName string
}

// Goal is a savings target (plan §7.3). Goals hold no money: progress is a
// view over the jar's available balance, filled in priority order.
type Goal struct {
	ID           string
	KidID        string
	JarID        string
	Name         string
	Emoji        string
	TargetAmount int64
	Priority     int // lower fills first
	CreatedBy    string
	ReachedAt    *time.Time // first time it was fully funded (notified once)
	ArchivedAt   *time.Time
	CreatedAt    time.Time
}

// InterestRule is parent-paid interest on one kid's jar (plan §7.4).
type InterestRule struct {
	KidID      string
	JarID      string
	MonthlyBPS int   // 100 = 1% a month
	MonthlyCap int64 // 0 = no cap
	Active     bool
}

// DatedAmount is a ledger amount with its business date, for balance history.
type DatedAmount struct {
	Amount      int64
	EffectiveAt time.Time
}

// Security is a stock or fund kids have looked at or bought (plan §7.6).
type Security struct {
	Symbol    string
	Name      string
	Kind      string // stock | fund
	UpdatedAt time.Time
}

// Quote is the last price seen for a symbol, in micro-dollars.
type Quote struct {
	Symbol    string
	Price     int64
	Open      int64
	PrevClose int64
	QuotedAt  time.Time // the market's time for the price
	FetchedAt time.Time // when Goldberry asked
}

type OrderSide string

const (
	SideBuy  OrderSide = "buy"
	SideSell OrderSide = "sell"
)

type OrderStatus string

const (
	OrderQueued    OrderStatus = "queued" // waiting for the market to open
	OrderFilled    OrderStatus = "filled"
	OrderCancelled OrderStatus = "cancelled"
)

// Order is a kid buying or selling. A buy has Amount (cash, minor units) and
// gets Shares when filled; a sell has Shares (micro-shares) and gets Proceeds
// and CostBasis. Price is the fill price in micro-dollars.
type Order struct {
	ID             string
	KidID          string
	JarID          string
	Symbol         string
	Side           OrderSide
	Amount         int64
	Shares         int64
	Status         OrderStatus
	Price          int64
	Proceeds       int64
	CostBasis      int64
	LedgerEntryID  string // a buy's debit, or a sale's credit
	PlacedBy       string
	IdempotencyKey string
	CreatedAt      time.Time
	FilledAt       *time.Time
	CancelledAt    *time.Time

	// Filled by list queries.
	Name         string
	SecurityKind string
}

// Payout is a sale's profit, owed by the parents until one of them pays it.
type Payout struct {
	ID            string
	KidID         string
	JarID         string
	OrderID       string
	Amount        int64
	Status        string // pending | paid
	PaidBy        string
	PaidAt        *time.Time
	LedgerEntryID string
	CreatedAt     time.Time

	// Filled by list queries.
	KidName    string
	Symbol     string
	PaidByName string
}

// Snapshot is a kid's investments at the end of a household day.
type Snapshot struct {
	KidID string
	Day   string // YYYY-MM-DD
	Value int64
	Cost  int64
}

// OrderFilter narrows ListOrders. Empty fields match everything.
type OrderFilter struct {
	KidID  string
	Status OrderStatus
	Limit  int
}
