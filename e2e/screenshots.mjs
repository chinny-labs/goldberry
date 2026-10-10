// Takes the README and website screenshots from the demo household
// (e2e/demo). `just screenshots` starts the demo and runs this:
//
//   BASE_URL=http://localhost:18081 OUT=../docs/screenshots node screenshots.mjs
//
// Desktop shots are 1440×900; phone shots are 390×844 at 2×.
import { chromium } from "playwright";
import { mkdir } from "node:fs/promises";

const base = process.env.BASE_URL ?? "http://localhost:18081";
const out = process.env.OUT ?? "../docs/screenshots";
await mkdir(out, { recursive: true });

const browser = await chromium.launch(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {});
const desktop = { viewport: { width: 1440, height: 900 } };
const phone = { viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true };

async function signIn(opts, username, secret, scheme = "light") {
  const ctx = await browser.newContext({ ...opts, colorScheme: scheme });
  const p = await ctx.newPage();
  await p.goto(base + "/login");
  await p.fill("#username", username);
  await p.click(".auth-card button[type=submit]");
  if (/^\d+$/.test(secret)) {
    await p.waitForSelector(".pin-pad button[data-digit]");
    for (const d of secret) await p.click(`.pin-pad button[data-digit="${d}"]`);
  } else {
    await p.fill("#secret", secret);
    await p.click(".auth-card button[type=submit]");
  }
  await p.waitForURL((u) => !u.pathname.startsWith("/login"));
  return p;
}

async function shot(p, name, path, { full = false, wait } = {}) {
  if (path) await p.goto(base + path);
  if (wait) await p.waitForSelector(wait);
  await p.evaluate(() => document.fonts.ready);
  await p.mouse.move(0, 0); // no hover state left over from the last click
  await p.screenshot({ path: `${out}/${name}.png`, fullPage: full });
  console.log(`· ${name}.png`);
}

const kidURL = async (p, name) => {
  await p.goto(base + "/admin");
  return new URL(await p.getAttribute(`.sidebar a.nav:has-text("${name}")`, "href"), base).pathname;
};

try {
  const mom = await signIn(desktop, "mom", "correct horse battery");
  const ava = await kidURL(mom, "Ava");
  await shot(mom, "parent-dashboard", "/admin");
  await shot(mom, "parent-kid", ava);
  await shot(mom, "parent-graph", ava + "/graph?range=3m&show=jars");
  // Mom's API tokens for Home Assistant and Claude: just the card, at 2×.
  const sharp = await signIn({ ...desktop, deviceScaleFactor: 2 }, "mom", "correct horse battery");
  await sharp.goto(base + "/admin/users");
  await sharp.click('main a:has-text("Mom")');
  await sharp.evaluate(() => document.fonts.ready);
  await sharp.locator("section.card", { has: sharp.locator("h2", { hasText: "API tokens" }) }).screenshot({ path: `${out}/api-tokens.png` });
  console.log("· api-tokens.png");

  const momDark = await signIn(desktop, "mom", "correct horse battery", "dark");
  await shot(momDark, "parent-dashboard-dark", "/admin");

  // The PIN pad, before signing in.
  const pin = await (await browser.newContext(phone)).newPage();
  await pin.goto(base + "/login");
  await pin.fill("#username", "ava");
  await pin.click(".auth-card button[type=submit]");
  await shot(pin, "pin-pad", null, { wait: ".pin-pad" });

  const kid = await signIn(phone, "ava", "2468");
  await shot(kid, "kid-home", "/");
  await shot(kid, "kid-graph", "/graph?range=3m");
  await shot(kid, "invest", "/invest");
  await shot(kid, "stock", "/invest/stocks/VTI");

  // Breaking her own lock on Save: the gauntlet, mid-countdown.
  await kid.goto(base + "/");
  await kid.click("a.lock-badge.lock-soft");
  await kid.waitForURL("**/gauntlet**");
  await kid.waitForTimeout(3200);
  await shot(kid, "gauntlet");

  const kidDark = await signIn(phone, "leo", "1357", "dark");
  await shot(kidDark, "kid-home-dark", "/");
} finally {
  await browser.close();
}
