# Loyalty programs: data sources

Probed on 2026-11-18 from a Swedish residential IP. "Working" means an
adapter in `internal/award/<key>/` returns real award availability.

## Working

| Program | Key | Source | Access | Availability |
|---|---|---|---|---|
| Alaska Atmos Rewards | `alaska` | `GET alaskaair.com/search/results/__data.json` (SvelteKit devalue) | Plain HTTP | Live, per flight; city codes; partner flights (AA, BA, …) |
| SAS EuroBonus | `sas` | `GET flysas.com/api/offers/flights?bookingFlow=points` | Plain HTTP; Cloudflare rejects Go's post-quantum TLS key share, so the client offers only X25519/P-256 | Live, per flight; standard awards (fixed price) and cash fares priced in points; taxes in DKK; no Star Alliance partners |
| JetBlue TrueBlue | `jetblue` | `POST jetblue.com/api/ecom/cb-flight-search/v1/search/NGB`, captured from the site's own page | Stealth headless Chrome (Fastly bot challenge blocks plain HTTP); ~16 s per search | Live, per flight; revenue-based points pricing |
| Virgin Atlantic Flying Club | `virgin` | `POST virginatlantic.com/reward-flight-finder/api/graphql` (reward seat checker) | Plain HTTP | Estimated: lowest points and taxes per day and cabin, no flights or times; own routes between airports only |

## Blocked

| Program | Why |
|---|---|
| Qatar Privilege Club | Award search needs a member login; Akamai Bot Manager |
| Emirates Skywards | "Log in to book Classic Rewards"; Akamai |
| British Airways Club | Akamai "high demand" page to curl and browser; Avios search needs login with hCaptcha and MFA |
| Iberia Plus | Akamai blocked the availability API after the first browser load; Avios search likely needs login |
| Finnair Plus | Akamai 403 on the award `offerList` call, even from Finnair's own page in stealth Chrome; open endpoints have cash prices only |
| Flying Blue | Akamai on plain HTTP; the reward flow redirects to a Flying Blue login |
| Lufthansa Miles & More | Member login required (account with at least 7,000 miles); Cloudflare |
| SAS partners (Star Alliance) | `bookingFlow=star` rejected |
| Turkish Miles & Smiles | No login needed, but PerimeterX CAPTCHA on the availability API and Akamai challenges |
| United MileagePlus | Akamai: anonymous token works, `FetchFlights` returns 428 Access Denied from automated Chrome |
| Delta SkyMiles | Akamai 444 on the GraphQL offer API, also from stealth Chrome |
| American AAdvantage | Akamai serves a cached decoy (`error 309`) or 403, also to a warmed headful browser |
| Air Canada Aeroplan | Akamai 403 to everything, including the homepage in stealth Chrome |
| Avianca LifeMiles | Public search endpoints removed; private ones need a member JWT |
| JAL Mileage Bank | Akamai Access Denied on the Amadeus award engine; JMB login required |
| ANA Mileage Club | Akamai block page; member login required |
| Singapore KrisFlyer | Akamai JS challenge and a "maintenance" page to the browser; member login required |
| Cathay Asia Miles | Queue-it waiting room and member login; Akamai on every host |

## What would unlock more

- **An aggregator API** (for example the seats.aero partner API): cached
  availability for Aeroplan, United, Flying Blue, Qatar, Virgin and others,
  which fits the model as `estimated`.
- **A user's own logged-in session** for the login-only programs, as
  point.me does.
- **A real, long-lived browser profile** attached over CDP for the Akamai
  programs that need no login (United, Aeroplan, American); demo-grade, not
  a service.
