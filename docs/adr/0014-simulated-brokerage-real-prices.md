# 0014. Investing is a simulated brokerage at real prices, with parent-paid profit

- **Status:** Accepted
- **Date:** 2026-10-10
- **Source:** [design plan](../design/plan.md) §7.6; [operations](../operations/investing.md)

## Decision

Kids invest the money in a fourth jar, **Invest**, which fills from the split rule like any other jar. Orders execute inside Goldberry at real US prices from a market-data provider (Finnhub, behind a `market.Provider` interface). No real brokerage is involved: like every other balance, the jar is money the parents hold for the kid.

- **A buy debits the jar when it's placed** (`invest_buy`). Holdings live in `orders`, not the ledger. A waiting order that's cancelled is a `reversal` of its debit, so the ledger stays append-only.
- **A sale credits at most its cost basis** (`invest_sell`), using average cost. A loss therefore comes off by itself. A profit becomes a `payouts` row that a parent pays (`invest_gain`), because the profit is new money the parents have to find, not money they already hold.
- **While the market is closed, orders queue** and fill at the next opening price. Buys still take the cash immediately, and sells reserve their shares.
- **Units are integers**: cents for cash, micro-dollars for prices, micro-shares for holdings. Share and price arithmetic goes through `math/big`, because a share count times a price overflows `int64`.
- **US dollars only.** The prices are USD, and converting currencies would put a moving exchange rate into a kid's balance.

## Consequences

Kids practise with their real allowance, and gains are meaningful without the app ever moving money. Parents control the risk through the Invest share of the split and by choosing when to pay earnings. Prices need outbound internet and a free API key, so investing is off by default and the app still runs offline without it. The SQLite migration rebuilds `jars` and `ledger_entries` to widen their CHECK constraints (copy, drop, rename, with foreign keys off), and a test upgrades a populated database through it.
