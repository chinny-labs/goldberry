package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chinny/goldberry/internal/buildinfo"
	"github.com/chinny/goldberry/internal/service"
)

// MCPPath is where the MCP server is mounted.
const MCPPath = "/mcp"

// MCPHandler serves the Model Context Protocol over streamable HTTP (ADR
// 0016). It uses the same bearer tokens, scopes, limits and service layer as
// the JSON API. Each request builds a server holding only the tools the token
// may use, so a read-only token never sees a money tool.
//
// The handler is stateless: every call is authenticated on its own, and no
// session outlives a token.
func (a *API) MCPHandler() http.Handler {
	inner := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		p, _ := r.Context().Value(principalKey{}).(*principal)
		if p == nil {
			return nil
		}
		return a.mcpServer(p)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, Logger: a.log})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, ok := a.authenticate(w, r)
		if !ok {
			return
		}
		if ok, wait := a.lim.allow(p.Token.ID); !ok {
			tooMany(w, wait)
			return
		}
		ctx := context.WithValue(r.Context(), principalKey{}, p)
		inner.ServeHTTP(w, r.WithContext(ctx))
	})
}

type principalKey struct{}

// toolError turns a service error into a tool failure the model can act on:
// a stable code, then the message.
func (a *API) toolError(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	item := -1
	var be *service.BatchError
	if errors.As(err, &be) {
		item, err = be.Index, be.Err
	}
	p := classify(err)
	if p.status == http.StatusInternalServerError {
		a.log.Error("mcp tool failed", "err", err)
		return errors.New("internal: something went wrong; it was logged")
	}
	msg := p.code + ": " + p.msg
	if item >= 0 {
		msg = p.code + ": item " + itoa(item+1) + ": " + p.msg
	}
	if p.field != "" {
		msg += " (field: " + p.field + ")"
	}
	return errors.New(msg)
}

func boolp(b bool) *bool { return &b }

// dryRun is true unless the caller said otherwise: money tools look before they leap.
func dryRun(p *bool) bool { return p == nil || *p }

// --- tool inputs ---------------------------------------------------------------

type kidArg struct {
	Kid string `json:"kid" jsonschema:"a kid's username (like ava) or ID"`
}

type listKidsIn struct {
	IncludeDisabled bool `json:"include_disabled,omitempty" jsonschema:"also list disabled kids"`
}

type ledgerIn struct {
	Kid   string `json:"kid" jsonschema:"a kid's username or ID"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many entries, newest first (default 50, max 500)"`
	JarID string `json:"jar_id,omitempty" jsonschema:"only this jar"`
	From  string `json:"from,omitempty" jsonschema:"earliest date, YYYY-MM-DD or RFC3339"`
	To    string `json:"to,omitempty" jsonschema:"latest date (exclusive), YYYY-MM-DD or RFC3339"`
}

type requestsIn struct {
	Kid    string `json:"kid,omitempty" jsonschema:"only this kid; omit for every kid"`
	Status string `json:"status,omitempty" jsonschema:"pending, approved, denied, cancelled or expired"`
	Limit  int    `json:"limit,omitempty" jsonschema:"how many, newest first (default 50)"`
}

type addJarIn struct {
	Kid  string `json:"kid" jsonschema:"a kid's username or ID"`
	Name string `json:"name" jsonschema:"the new jar's name"`
}

type renameJarIn struct {
	JarID string `json:"jar_id"`
	Name  string `json:"name"`
}

type splitIn2 struct {
	Kid    string         `json:"kid" jsonschema:"a kid's username or ID"`
	Shares map[string]int `json:"shares" jsonschema:"jar ID to share in basis points (10000 = 100%). Include every jar; the total must be 10000"`
}

type goalIn2 struct {
	Kid    string `json:"kid" jsonschema:"a kid's username or ID"`
	Name   string `json:"name"`
	Target int64  `json:"target" jsonschema:"the price in minor units (cents for USD)"`
	Emoji  string `json:"emoji,omitempty"`
	JarID  string `json:"jar_id,omitempty" jsonschema:"the jar that funds it (default: Save)"`
}

type goalIDIn struct {
	GoalID string `json:"goal_id"`
}

const moneyNote = " Amounts are integers in minor units (cents for USD). " +
	"Defaults to a dry run that changes nothing and shows what would happen; set dry_run to false to do it. " +
	"The idempotency_key makes a retry safe: reuse the same key to retry, and pick a stable key that names the payment (like allowance-2026-10-ava)."

type postEntryIn struct {
	Kid            string `json:"kid" jsonschema:"a kid's username or ID"`
	Amount         int64  `json:"amount" jsonschema:"positive minor units"`
	Direction      string `json:"direction,omitempty" jsonschema:"add (default) or remove"`
	JarID          string `json:"jar_id,omitempty" jsonschema:"the jar (default: the kid's first jar, Spend)"`
	UseSplit       bool   `json:"use_split,omitempty" jsonschema:"deposits only: split across jars by the kid's split"`
	Comment        string `json:"comment,omitempty" jsonschema:"the kid sees this"`
	PrivateNote    string `json:"private_note,omitempty" jsonschema:"parents only"`
	IdempotencyKey string `json:"idempotency_key" jsonschema:"a stable key for this payment"`
	DryRun         *bool  `json:"dry_run,omitempty" jsonschema:"default true; false actually posts"`
}

type postEntriesIn struct {
	Entries []postEntryItem `json:"entries" jsonschema:"1 to 100 entries, applied in order, all or nothing"`
	DryRun  *bool           `json:"dry_run,omitempty" jsonschema:"default true; false actually posts"`
}

type postEntryItem struct {
	Kid            string `json:"kid" jsonschema:"a kid's username or ID"`
	Amount         int64  `json:"amount" jsonschema:"positive minor units"`
	Direction      string `json:"direction,omitempty" jsonschema:"add (default) or remove"`
	JarID          string `json:"jar_id,omitempty"`
	UseSplit       bool   `json:"use_split,omitempty"`
	Comment        string `json:"comment,omitempty"`
	PrivateNote    string `json:"private_note,omitempty"`
	IdempotencyKey string `json:"idempotency_key" jsonschema:"a stable key for this entry; unique within the batch"`
}

type reverseIn struct {
	EntryID string `json:"entry_id"`
	DryRun  *bool  `json:"dry_run,omitempty" jsonschema:"default true: show the entry that would be reversed; false reverses it"`
}

type decideIn struct {
	RequestID      string `json:"request_id"`
	Decision       string `json:"decision" jsonschema:"approve or deny"`
	ApprovedAmount int64  `json:"approved_amount,omitempty" jsonschema:"approve less than asked, in minor units; omit for the full amount"`
	Note           string `json:"note,omitempty" jsonschema:"shown to the kid"`
	DryRun         *bool  `json:"dry_run,omitempty" jsonschema:"default true: show the request; false decides it"`
}

// --- tool outputs --------------------------------------------------------------

type previewOut struct {
	DryRun  bool         `json:"dry_run"`
	Results []entriesOut `json:"results"`
}

type reverseOut struct {
	DryRun  bool       `json:"dry_run"`
	Entry   *entryDTO  `json:"entry,omitempty" jsonschema:"the entry that would be reversed (dry run)"`
	Entries []entryDTO `json:"entries,omitempty" jsonschema:"the reversals posted"`
}

type decideOut struct {
	DryRun  bool       `json:"dry_run"`
	Request requestDTO `json:"request"`
}

// mcpServer builds the server for one authenticated request.
func (a *API) mcpServer(p *principal) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "goldberry", Version: buildinfo.Version}, &mcp.ServerOptions{
		Instructions: "Goldberry is a family allowance tracker. Kids are addressed by username. " +
			"Money is in minor units (cents for USD); call get_household for the currency. " +
			"Money tools default to dry runs: show the user what would happen, then repeat with dry_run=false to do it.",
	})
	// Every call runs as the token's owner, with the token named in the audit trail.
	ctxFor := func(ctx context.Context) context.Context { return service.WithToken(ctx, p.Token.ID) }
	read := &mcp.ToolAnnotations{ReadOnlyHint: true}
	idemWrite := &mcp.ToolAnnotations{DestructiveHint: boolp(false), IdempotentHint: true}
	money := &mcp.ToolAnnotations{DestructiveHint: boolp(true)}

	if service.HasScope(p.Token, service.ScopeRead) {
		mcp.AddTool(s, &mcp.Tool{Name: "get_household", Description: "The household's name, currency and time zone.", Annotations: read},
			func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, householdDTO, error) {
				h, cur, err := a.svc.Household(ctxFor(ctx))
				return nil, household(h, cur), a.toolErrOrNil(err)
			})
		mcp.AddTool(s, &mcp.Tool{Name: "list_kids", Description: "Every kid with their total money, what is held for pending requests, and what is available.", Annotations: read},
			func(ctx context.Context, _ *mcp.CallToolRequest, in listKidsIn) (*mcp.CallToolResult, list[kidDTO], error) {
				v, err := a.listKids(ctxFor(ctx), in.IncludeDisabled)
				return nil, v, a.toolErrOrNil(err)
			})
		mcp.AddTool(s, &mcp.Tool{Name: "get_kid", Description: "One kid with each jar's balance, holds, split share and locks.", Annotations: read},
			func(ctx context.Context, _ *mcp.CallToolRequest, in kidArg) (*mcp.CallToolResult, kidDetailDTO, error) {
				v, err := a.getKid(ctxFor(ctx), in.Kid)
				return nil, v, a.toolErrOrNil(err)
			})
		mcp.AddTool(s, &mcp.Tool{Name: "get_ledger", Description: "A kid's ledger, newest first. Amounts are signed: credits positive, debits negative.", Annotations: read},
			func(ctx context.Context, _ *mcp.CallToolRequest, in ledgerIn) (*mcp.CallToolResult, list[entryDTO], error) {
				v, err := a.getLedger(ctxFor(ctx), in.Kid, ledgerQuery{Limit: in.Limit, JarID: in.JarID, From: in.From, To: in.To})
				return nil, v, a.toolErrOrNil(err)
			})
		mcp.AddTool(s, &mcp.Tool{Name: "list_goals", Description: "A kid's savings goals with how funded each is.", Annotations: read},
			func(ctx context.Context, _ *mcp.CallToolRequest, in kidArg) (*mcp.CallToolResult, list[goalDTO], error) {
				v, err := a.getGoals(ctxFor(ctx), in.Kid)
				return nil, v, a.toolErrOrNil(err)
			})
		mcp.AddTool(s, &mcp.Tool{Name: "list_requests", Description: "Withdrawal requests, newest first. Use status=pending for the ones waiting for a decision.", Annotations: read},
			func(ctx context.Context, _ *mcp.CallToolRequest, in requestsIn) (*mcp.CallToolResult, list[requestDTO], error) {
				v, err := a.getRequests(ctxFor(ctx), in.Kid, in.Status, in.Limit)
				return nil, v, a.toolErrOrNil(err)
			})
	}

	if service.HasScope(p.Token, service.ScopeWrite) {
		mcp.AddTool(s, &mcp.Tool{Name: "add_jar", Description: "Add a custom jar to a kid. It starts with a 0% share of the split.", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolp(false)}},
			func(ctx context.Context, _ *mcp.CallToolRequest, in addJarIn) (*mcp.CallToolResult, jarDTO, error) {
				ctx = ctxFor(ctx)
				k, err := a.svc.ResolveKid(ctx, in.Kid)
				if err != nil {
					return nil, jarDTO{}, a.toolError(err)
				}
				j, err := a.svc.AddJar(ctx, p.User, k.ID, in.Name)
				if err != nil {
					return nil, jarDTO{}, a.toolError(err)
				}
				jv, err := a.svc.JarByID(ctx, j.ID)
				return nil, jar(jv), a.toolErrOrNil(err)
			})
		mcp.AddTool(s, &mcp.Tool{Name: "rename_jar", Description: "Rename a jar.", Annotations: idemWrite},
			func(ctx context.Context, _ *mcp.CallToolRequest, in renameJarIn) (*mcp.CallToolResult, jarDTO, error) {
				ctx = ctxFor(ctx)
				if err := a.svc.RenameJar(ctx, p.User, in.JarID, in.Name); err != nil {
					return nil, jarDTO{}, a.toolError(err)
				}
				jv, err := a.svc.JarByID(ctx, in.JarID)
				return nil, jar(jv), a.toolErrOrNil(err)
			})
		mcp.AddTool(s, &mcp.Tool{Name: "set_split", Description: "Replace how a kid's deposits and allowance divide across jars. Shares are in basis points and must total 10000.", Annotations: idemWrite},
			func(ctx context.Context, _ *mcp.CallToolRequest, in splitIn2) (*mcp.CallToolResult, list[jarDTO], error) {
				ctx = ctxFor(ctx)
				k, err := a.svc.ResolveKid(ctx, in.Kid)
				if err != nil {
					return nil, list[jarDTO]{}, a.toolError(err)
				}
				if err := a.svc.SetSplit(ctx, p.User, k.ID, in.Shares); err != nil {
					return nil, list[jarDTO]{}, a.toolError(err)
				}
				v, err := a.getJars(ctx, k.ID)
				return nil, v, a.toolErrOrNil(err)
			})
		mcp.AddTool(s, &mcp.Tool{Name: "create_goal", Description: "Add a savings goal for a kid. The goal holds no money; it shows progress of a jar.", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolp(false)}},
			func(ctx context.Context, _ *mcp.CallToolRequest, in goalIn2) (*mcp.CallToolResult, goalDTO, error) {
				ctx = ctxFor(ctx)
				k, err := a.svc.ResolveKid(ctx, in.Kid)
				if err != nil {
					return nil, goalDTO{}, a.toolError(err)
				}
				g, err := a.svc.CreateGoal(ctx, p.User, k.ID, service.GoalInput{Name: in.Name, Emoji: in.Emoji, Target: in.Target, JarID: in.JarID})
				if err != nil {
					return nil, goalDTO{}, a.toolError(err)
				}
				gv, err := a.svc.GoalByID(ctx, g.ID)
				return nil, goal(gv), a.toolErrOrNil(err)
			})
		mcp.AddTool(s, &mcp.Tool{Name: "archive_goal", Description: "Remove a goal from view. Its history stays.", Annotations: idemWrite},
			func(ctx context.Context, _ *mcp.CallToolRequest, in goalIDIn) (*mcp.CallToolResult, goalDTO, error) {
				ctx = ctxFor(ctx)
				if err := a.svc.ArchiveGoal(ctx, p.User, in.GoalID); err != nil {
					return nil, goalDTO{}, a.toolError(err)
				}
				gv, err := a.svc.GoalByID(ctx, in.GoalID)
				return nil, goal(gv), a.toolErrOrNil(err)
			})
	}

	if service.HasScope(p.Token, service.ScopeMoney) {
		mcp.AddTool(s, &mcp.Tool{Name: "post_entry", Description: "Add money to, or remove money from, one kid's jar." + moneyNote, Annotations: money},
			func(ctx context.Context, _ *mcp.CallToolRequest, in postEntryIn) (*mcp.CallToolResult, previewOut, error) {
				return a.mcpPost(ctxFor(ctx), p, []postEntryItem{{in.Kid, in.Amount, in.Direction, in.JarID, in.UseSplit, in.Comment, in.PrivateNote, in.IdempotencyKey}}, in.DryRun)
			})
		mcp.AddTool(s, &mcp.Tool{Name: "post_entries", Description: "Post up to 100 entries for one or more kids in one go. All succeed or none do; an error names the item to fix." + moneyNote, Annotations: money},
			func(ctx context.Context, _ *mcp.CallToolRequest, in postEntriesIn) (*mcp.CallToolResult, previewOut, error) {
				return a.mcpPost(ctxFor(ctx), p, in.Entries, in.DryRun)
			})
		mcp.AddTool(s, &mcp.Tool{Name: "reverse_entry", Description: "Undo a ledger entry by posting its opposite (a split deposit reverses together). Defaults to a dry run that shows the entry.", Annotations: money},
			func(ctx context.Context, _ *mcp.CallToolRequest, in reverseIn) (*mcp.CallToolResult, reverseOut, error) {
				ctx = ctxFor(ctx)
				if dryRun(in.DryRun) {
					e, err := a.svc.EntryByID(ctx, in.EntryID)
					if err != nil {
						return nil, reverseOut{}, a.toolError(err)
					}
					d := entry(e)
					return nil, reverseOut{DryRun: true, Entry: &d}, nil
				}
				es, err := a.svc.Reverse(ctx, p.User, in.EntryID)
				return nil, reverseOut{Entries: entries(es)}, a.toolErrOrNil(err)
			})
		mcp.AddTool(s, &mcp.Tool{Name: "decide_request", Description: "Approve (optionally for less) or deny a kid's withdrawal request. Approving posts the withdrawal. Defaults to a dry run that shows the request.", Annotations: money},
			func(ctx context.Context, _ *mcp.CallToolRequest, in decideIn) (*mcp.CallToolResult, decideOut, error) {
				ctx = ctxFor(ctx)
				if in.Decision != "approve" && in.Decision != "deny" {
					return nil, decideOut{}, a.toolError(&service.UserError{Field: "decision", Msg: "decision must be approve or deny."})
				}
				if dryRun(in.DryRun) {
					r, err := a.svc.RequestByID(ctx, in.RequestID)
					return nil, decideOut{DryRun: true, Request: request(r)}, a.toolErrOrNil(err)
				}
				r, err := a.svc.DecideRequest(ctx, p.User, in.RequestID, service.Decision{
					Approve: in.Decision == "approve", ApprovedAmount: in.ApprovedAmount, Note: in.Note})
				return nil, decideOut{Request: request(r)}, a.toolErrOrNil(err)
			})
	}
	return s
}

func (a *API) toolErrOrNil(err error) error {
	if err == nil {
		return nil
	}
	return a.toolError(err)
}

// mcpPost posts items as a batch; one item is a batch of one.
func (a *API) mcpPost(ctx context.Context, p *principal, items []postEntryItem, dry *bool) (*mcp.CallToolResult, previewOut, error) {
	inputs := make([]service.EntryInput, 0, len(items))
	for i, it := range items {
		fail := func(err error) (*mcp.CallToolResult, previewOut, error) {
			return nil, previewOut{}, a.toolError(&service.BatchError{Index: i, Err: err})
		}
		k, err := a.svc.ResolveKid(ctx, it.Kid)
		if err != nil {
			return fail(err)
		}
		key, err := cleanKey(it.IdempotencyKey)
		if err != nil {
			return fail(err)
		}
		ei, err := entryIn{Direction: it.Direction, Amount: it.Amount, JarID: it.JarID, UseSplit: it.UseSplit,
			Comment: it.Comment, PrivateNote: it.PrivateNote}.input(k.ID, key)
		if err != nil {
			return fail(err)
		}
		inputs = append(inputs, ei)
	}
	d := dryRun(dry)
	res, err := a.svc.PostEntries(ctx, p.User, inputs, d)
	if err != nil {
		return nil, previewOut{}, a.toolError(err)
	}
	out := previewOut{DryRun: d, Results: make([]entriesOut, 0, len(res))}
	for _, r := range res {
		out.Results = append(out.Results, entriesOut{Entries: entries(r.Entries), Replayed: r.Replayed})
	}
	return nil, out, nil
}
