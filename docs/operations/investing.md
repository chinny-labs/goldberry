# Investing

Kids can buy and sell real US stocks and funds (ETFs) at real market prices, using the money in a fourth **Invest** jar. Nothing is bought on a real exchange: Goldberry records the trades in its own ledger at the prices the market shows, the same way every other balance in Goldberry is money the parents hold for the kid. See the [design plan](../design/plan.md) §7.6 and [ADR 0014](../adr/0014-simulated-brokerage-real-prices.md).

## Turn it on

1. Get a free API key at [finnhub.io](https://finnhub.io/register). The free tier allows 60 calls a minute, far more than a household needs.
2. Give it to Goldberry and restart:
   - **Compose:** `GOLDBERRY_FINNHUB_API_KEY=…` in `environment`, or `GOLDBERRY_FINNHUB_API_KEY_FILE` pointing at a secret (see `deploy/compose/compose.yaml`).
   - **Helm:** `--set investing.finnhubKey=…`, or put the key in your own Secret and set `investing.existingSecret` (and `investing.existingSecretKey`, default `finnhub-key`).
3. Optional: set `GOLDBERRY_SEC_CONTACT` (Helm: `investing.secContact`) to your email to show **What's inside** on fund pages (see *Network and data*). The SEC asks every automated client to identify itself.
4. In **Settings → Investing**, choose **Turn investing on**.

Turning it on gives every kid an Invest jar. Kids still on the standard 70/20/10 split move to **50% Spend, 20% Save, 10% Give, 20% Invest**, so part of every deposit and allowance lands in Invest by itself. Kids with a split you've customised keep it; set their Invest share on their **Jars and split** page. Kids added while investing is on start with four jars.

Investing needs a household that uses **US dollars**, because the prices are US prices.

## How the money works

| Kid does | Ledger |
| --- | --- |
| Buys $5 of a stock | `invest_buy` −$5 from the Invest jar, straight away |
| Cancels an order still waiting for the market | a `reversal` of that debit: +$5 |
| Sells for **more** than they paid | `invest_sell` + what they paid. The profit is recorded as **earnings owed** and waits for a parent. |
| Sells for **less** than they paid | `invest_sell` + what it sold for. The loss comes off by itself. |
| — | A parent pays earnings: `invest_gain` + the profit, posted by that parent |

Parents are told about earnings in the bell. Pay them from the dashboard or the kid's page. Paying records that you handed over the money, just as approving a request does.

Investing entries can't be undone from the ledger. To unwind one, sell, or cancel a waiting order.

## Market hours

The US market is open 9:30 a.m. to 4 p.m. New York time, Monday to Friday, except holidays (Goldberry asks Finnhub). While it's open, orders fill at the current price. While it's closed, they **wait**: buys take the cash at once, sells set the shares aside, and the background job fills them at the next **opening price**, like a real broker's market-on-open order. Kids can cancel a waiting order until then.

Kids buy by **dollar amount**, so they get a slice of a share. Shares are tracked to a millionth, and the smallest order is $1.00.

## Network and data

- The container needs outbound HTTPS to `finnhub.io`. If your cluster restricts egress, allow it.
- Fund pages show **What's inside**: the ten biggest holdings and their share of the fund. That comes from the monthly N-PORT reports funds file with the SEC, so the container also needs outbound HTTPS to `www.sec.gov`, and `GOLDBERRY_SEC_CONTACT` must be set (your email goes in the User-Agent; the SEC blocks requests without one). The section is left out if the variable is unset, the SEC can't be reached, or it has no report for the fund (some, like SPY, don't file one). The numbers are as of the report date (a month-end, filed up to two months later), not today. Each fund is fetched at most once a day and kept in memory, and no key or setup is needed.
- Quotes are cached for a minute while the market is open and 30 minutes while it's closed, so page views don't each cost an API call. The key is sent in a header (`X-Finnhub-Token`), not in the URL.
- Every five minutes, the job records each investing kid's portfolio value for the day (one point a day, the last one wins) for the **How it's grown** graph.
- `goldberry export` includes orders, earnings and snapshots. Cached quotes are left out and fetched again.

## Turning it off

Holdings, history and Invest jars stay. Orders waiting for the market are cancelled and their cash refunded. Kids stop seeing the Invest tab, and nobody can trade until it's back on.

## Troubleshooting

- **"Set GOLDBERRY_FINNHUB_API_KEY first"** in Settings: the variable isn't reaching the container. Check `docker compose config` or `kubectl describe pod`.
- **"Couldn't get prices right now"**: Finnhub is unreachable, refused the key (the log says `market: the API key was refused`), or the rate limit was hit. Pages fall back to the last price seen and mark it as old. A waiting order never fills at an old price; it waits for a fresh one.
- **An order is still waiting after the open**: the job runs every minute. Check the log for `job failed` with `job=invest-fill`.
