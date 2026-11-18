package cathay

import (
	"slices"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// band is one distance band of the Asia Miles Standard award chart for
// Cathay-operated flights: the longest route it covers and its one-way
// price per passenger in each cabin, 0 where the cabin is not sold.
type band struct {
	upTo  float64
	miles map[award.Cabin]int
}

// chart is the one-way Standard award chart for Cathay-operated flights in
// force since the repricing of 1 May 2026. Cathay no longer publishes it;
// the figures are reconstructed from live searches. The 751 to 2,750 mile
// band is split: routes touching a typeTwo country price higher.
var chart = []band{
	{750, prices(7000, 11000, 16000, 0)},
	{2750, prices(9000, 18000, 27000, 43000)},
	{5000, prices(20000, 39000, 60000, 90000)},
	{7500, prices(27000, 52000, 91000, 125000)},
	{1e9, prices(38000, 78000, 119000, 160000)},
}

// typeTwoBand is the price of the 751 to 2,750 mile band for routes that
// touch a typeTwo country.
var typeTwoBand = prices(13000, 23000, 33000, 50000)

// typeTwo are the countries that put a 751 to 2,750 mile route in the
// higher Type 2 band: Bangladesh, India, Indonesia, Japan, Nepal and Sri
// Lanka.
var typeTwo = []string{"BD", "IN", "ID", "JP", "NP", "LK"}

// prices maps the four cabin prices, from economy up, to their cabins.
func prices(economy, premium, business, first int) map[award.Cabin]int {
	return map[award.Cabin]int{
		award.Economy:  economy,
		award.Premium:  premium,
		award.Business: business,
		award.First:    first,
	}
}

// chartMiles returns the one-way Standard award miles per passenger from a
// to b in cabin, 0 when that band does not sell the cabin.
func chartMiles(a, b airport, cabin award.Cabin) int {
	d := distance(a, b)
	for i, bd := range chart {
		if d > bd.upTo {
			continue
		}
		if i == 1 && (slices.Contains(typeTwo, a.country) ||
			slices.Contains(typeTwo, b.country)) {
			return typeTwoBand[cabin]
		}
		return bd.miles[cabin]
	}
	return 0
}

// Carrier surcharges Cathay adds to an award per sector and passenger, in
// Hong Kong dollars, from 1 August 2026.
const (
	surchargeLongHaul   = 1362
	surchargeSouthAsia  = 633
	surchargeMainland   = 198
	surchargeOtherRoute = 339
)

// southAsia are the countries whose routes to Hong Kong carry the South
// Asia surcharge.
var southAsia = []string{"IN", "NP", "LK", "BD", "MV"}

// longHaulMiles is the distance beyond which a route carries the long-haul
// surcharge.
const longHaulMiles = 2750

// surcharge returns the carrier surcharge per passenger for the sector
// from origin a to destination b, whose codes are from and to.
func surcharge(from, to string, a, b airport) float64 {
	other := b
	if from != "HKG" && to == "HKG" {
		other = a
	}
	switch {
	case distance(a, b) > longHaulMiles:
		return surchargeLongHaul
	case slices.Contains(southAsia, other.country):
		return surchargeSouthAsia
	case other.country == "CN":
		return surchargeMainland
	}
	return surchargeOtherRoute
}
