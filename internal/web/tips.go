package web

import "time"

// Investing tips for kids (plan §7.6). Each investing word or number on a
// page carries a "?" that opens its tip; /invest/learn lists them all. Write
// them for a ten-year-old: short sentences, everyday words, no promises.

type tip struct {
	Key   string // anchor on /invest/learn
	Term  string
	Ask   string // the "?" button's label for screen readers
	Text  string
	Emoji string
}

var tipList = []tip{
	{"stock", "Stock", "What is a stock?", "A stock is a tiny piece of a company. If you own some Apple stock, you own a little bit of Apple! When the company does well, your piece can be worth more. When it struggles, it can be worth less.", "🏢"},
	{"share", "Share", "What is a share?", "A share is one piece of a company's stock. You don't need a whole one: like a slice of pizza instead of the whole pie, you can own part of a share.", "🍕"},
	{"fund", "Fund (ETF)", "What is a fund?", "A fund is a basket that holds lots of companies at once. Buy one fund and you own a little bit of all of them, so one company having a bad year can't sink you.", "🧺"},
	{"symbol", "Symbol", "What is a symbol?", "A symbol (people also say ticker) is a short nickname a company uses on the stock market, like AAPL for Apple or DIS for Disney.", "🏷️"},
	{"price", "Price", "What is the price?", "The price is what one whole share costs right now. It changes all day while the market is open, because people are always buying and selling.", "💲"},
	{"market", "Market hours", "When is the market open?", "The US stock market is open Monday to Friday, 9:30 a.m. to 4 p.m. New York time, except holidays. If you buy or sell while it's closed, your order waits and goes through when it opens.", "🕤"},
	{"waiting", "Waiting order", "What is a waiting order?", "The market was closed, so your order is waiting in line. It goes through at the opening price when the market opens. Changed your mind? You can cancel it until then.", "⏳"},
	{"worth", "Worth now", "What does worth now mean?", "What your investments would sell for right now, at today's prices.", "📈"},
	{"paid", "What you paid", "What does what you paid mean?", "How much money you put in. Compare it with what it's worth now to see if you're up or down.", "🧾"},
	{"gain", "Gain or loss", "What is a gain or loss?", "A gain is how much more your investment is worth than what you paid. A loss is when it's worth less. It only becomes real when you sell, so a loss can turn around if you wait.", "⚖️"},
	{"today", "Today", "What does today mean?", "How much the price moved since the market closed yesterday. Little ups and downs every day are normal.", "📅"},
	{"earnings", "Earnings", "What are earnings?", "When you sell for more than you paid, the extra is your profit. What you paid comes back to your jar right away, and a grown-up pays the profit into your jar.", "🎉"},
	{"jar", "Invest jar", "What is the Invest jar?", "Your Invest jar holds money for investing. Part of every allowance lands in it by itself. Money waiting in the jar isn't invested yet: pick something to buy!", "🫙"},
	{"fraction", "Buying by dollars", "Why buy in dollars?", "Some shares cost hundreds of dollars. You don't need that much: pick how many dollars to spend and you get that much of a share.", "✂️"},
	{"diversify", "Don't put all your eggs in one basket", "What does diversify mean?", "Owning lots of different companies means a bad day for one of them hurts less. Funds do this for you.", "🥚"},
	{"risk", "Risk", "What is risk?", "Investing can lose money, and nobody knows for sure what prices will do next. Only invest money you won't need soon.", "🎢"},
}

var tipsByKey = func() map[string]tip {
	m := map[string]tip{}
	for _, t := range tipList {
		m[t.Key] = t
	}
	return m
}()

// tricks are longer-lived habits, shown one a day on the invest page.
var tricks = []string{
	"Start with a fund. One fund owns lots of companies, so you're not betting on just one.",
	"Buy companies you understand. What do you and your friends use every day?",
	"Don't panic when prices drop. Selling while it's down turns a dip into a real loss.",
	"Time is your superpower. The longer you leave money invested, the more time it has to grow.",
	"Check in once a week, not every hour. Prices wiggle all the time.",
	"Never invest money you'll need soon, like for something you're buying next month.",
	"A company being famous doesn't make its stock a good buy. Ask: will it grow?",
	"What went up last year isn't promised to go up again.",
	"Small amounts add up. A little from every allowance can grow into a lot.",
}

// trickOfTheDay picks the same trick all day.
func trickOfTheDay(now time.Time) string { return tricks[now.YearDay()%len(tricks)] }

// starter picks: shown before a kid has searched for anything.
type pick struct{ Symbol, Name, Why string }

var starterFunds = []pick{
	{"VTI", "Vanguard Total Stock Market ETF", "Almost every company in the US, in one fund."},
	{"VOO", "Vanguard S&P 500 ETF", "The 500 biggest US companies."},
	{"VXUS", "Vanguard Total International Stock ETF", "Companies outside the US, all over the world."},
	{"QQQ", "Invesco QQQ Trust", "100 big companies, lots of them tech."},
}

var starterStocks = []pick{
	{"AAPL", "Apple", "iPhones, iPads and Macs."},
	{"DIS", "Disney", "Movies, Disney+ and theme parks."},
	{"NKE", "Nike", "Sneakers and sportswear."},
	{"MCD", "McDonald's", "Burgers and fries, everywhere."},
	{"NFLX", "Netflix", "Shows and movies to stream."},
	{"RBLX", "Roblox", "The game where players build the games."},
}
