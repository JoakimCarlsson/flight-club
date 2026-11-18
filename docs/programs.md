# Loyalty programs: data sources

| Program | Source | Access | Status |
|---|---|---|---|
| Alaska (Atmos Rewards) | `GET https://www.alaskaair.com/search/results/__data.json?O=&D=&OD=&A=&RT=false&ShoppingMethod=onlineaward`, SvelteKit devalue stream | Plain HTTP, no session, no login | Working: live seats, points and taxes per passenger and total, partner fares, city codes |
| Finnair Plus | `POST https://api.finnair.com/d/fcom/offers-prod/current/api/offerList` (`isAward: true`, `x-dd-flow-type: award`) | Akamai answers 403 to curl, a plain Chromium and stealth Chrome (headless and headful) from this network | Blocked; needs a residential IP or an Akamai sensor solution to evaluate further |

Not yet probed: British Airways, Flying Blue, Qatar, Iberia, Aeroplan, Lifemiles, JAL, United, Delta, American, Turkish, ANA.
