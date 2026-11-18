package delta

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// defaultCurrency is the currency taxes are taken to be in when the answer
// does not name one; delta.com prices US searches in dollars.
const defaultCurrency = "USD"

// flexString is a JSON value the API sends as a string or a number, read as
// its text either way.
type flexString string

// UnmarshalJSON reads a JSON string, number or null into s. It returns an
// error for any other JSON value.
func (s *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case bytes.Equal(b, []byte("null")):
		*s = ""
		return nil
	case len(b) > 0 && b[0] == '"':
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = flexString(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("delta: %s is not a string or number", b)
	}
	*s = flexString(n.String())
	return nil
}

// answer is a GraphQL answer of the offer API.
type answer struct {
	// Data holds the search, nil when the search failed outright.
	Data *struct {
		// GqlSearchOffers is the search's result.
		GqlSearchOffers *searchOffers `json:"gqlSearchOffers"`
	} `json:"data"`
	// Errors are GraphQL errors, such as "no flights" for a route Delta
	// does not sell.
	Errors []struct {
		// Message says what went wrong.
		Message string `json:"message"`
	} `json:"errors"`
}

// searchOffers is the result of one search.
type searchOffers struct {
	// GqlOffersSets are the itineraries, each with an offer per column.
	GqlOffersSets []offerSet `json:"gqlOffersSets"`
	// OfferDataList describes the fare columns.
	OfferDataList *struct {
		// RetailItemDefinitionList names the fare columns, in the order of
		// every offer set's offers.
		RetailItemDefinitionList []column `json:"retailItemDefinitionList"`
	} `json:"offerDataList"`
}

// column is one fare column of the results.
type column struct {
	// RetailItemBrandID is the column's brand, e.g. "CMAIN" or "CD1".
	RetailItemBrandID string `json:"retailItemBrandId"`
	// RetailItemPriorityText is the column's rank, which names its cabin.
	RetailItemPriorityText flexString `json:"retailItemPriorityText"`
}

// offerSet is one itinerary and its offer in every column.
type offerSet struct {
	// Trips holds the itinerary, one trip for a one-way.
	Trips []trip `json:"trips"`
	// Offers are its prices, one per column in column order.
	Offers []fareOffer `json:"offers"`
}

// trip is one itinerary.
type trip struct {
	// TotalTripTime is the door-to-door time.
	TotalTripTime *struct {
		// HourCnt is the whole hours, past 24 for a long trip.
		HourCnt int `json:"hourCnt"`
		// MinuteCnt is the minutes past the hours.
		MinuteCnt int `json:"minuteCnt"`
	} `json:"totalTripTime"`
	// FlightSegment are its flights, in order.
	FlightSegment []segment `json:"flightSegment"`
}

// segment is one flight of an itinerary.
type segment struct {
	// FlightSegmentNum numbers the flight within the trip, from 1.
	FlightSegmentNum flexString `json:"flightSegmentNum"`
	// OriginAirportCode is the IATA code it leaves from.
	OriginAirportCode string `json:"originAirportCode"`
	// DestinationAirportCode is the IATA code it lands at.
	DestinationAirportCode string `json:"destinationAirportCode"`
	// ScheduledDepartureLocalTs is the local departure, without offset.
	ScheduledDepartureLocalTs string `json:"scheduledDepartureLocalTs"`
	// ScheduledArrivalLocalTs is the local arrival, without offset.
	ScheduledArrivalLocalTs string `json:"scheduledArrivalLocalTs"`
	// MarketingCarrier is the airline selling it.
	MarketingCarrier carrier `json:"marketingCarrier"`
	// OperatingCarrier is the airline flying it, nil when not said.
	OperatingCarrier *carrier `json:"operatingCarrier"`
	// Aircraft is the aircraft type, nil when not said.
	Aircraft *struct {
		// FleetTypeCode is Delta's code for the type, e.g. "359".
		FleetTypeCode string `json:"fleetTypeCode"`
		// SubFleetTypeCode is the sub-fleet code.
		SubFleetTypeCode string `json:"subFleetTypeCode"`
	} `json:"aircraft"`
}

// carrier is an airline and a flight number.
type carrier struct {
	// CarrierCode is the IATA airline code.
	CarrierCode string `json:"carrierCode"`
	// CarrierNum is the flight number.
	CarrierNum flexString `json:"carrierNum"`
}

// fareOffer is an itinerary's price in one column.
type fareOffer struct {
	// SoldOut is set when the column has no seat for it.
	SoldOut bool `json:"soldOut"`
	// AdditionalOfferProperties says whether it is for sale.
	AdditionalOfferProperties *struct {
		// UnavailableForSale is set when it cannot be bought.
		UnavailableForSale bool `json:"unavailableForSale"`
		// DominantSegmentBrandID is the brand of the fare as a whole.
		DominantSegmentBrandID string `json:"dominantSegmentBrandId"`
	} `json:"additionalOfferProperties"`
	// OfferItems hold the fare.
	OfferItems []struct {
		// RetailItems hold the fare.
		RetailItems []struct {
			// RetailItemMetaData holds the fare.
			RetailItemMetaData struct {
				// FareInformation is the fare, one entry.
				FareInformation []fareInfo `json:"fareInformation"`
			} `json:"retailItemMetaData"`
		} `json:"retailItems"`
	} `json:"offerItems"`
}

// fareInfo is a fare's brands, seats and price.
type fareInfo struct {
	// BrandByFlightLegs is the brand each segment is sold in.
	BrandByFlightLegs []struct {
		// BrandID is the brand, e.g. "CD1" or a partner's "CVSUP".
		BrandID string `json:"brandId"`
		// FlightSegmentNum is the segment it is for.
		FlightSegmentNum flexString `json:"flightSegmentNum"`
		// CosCode is the fare class letter, empty when not said.
		CosCode string `json:"cosCode"`
	} `json:"brandByFlightLegs"`
	// AvailableSeatCnt is how many seats are left, nil when not said.
	AvailableSeatCnt *int `json:"availableSeatCnt"`
	// FarePrice is the price for one passenger, one entry.
	FarePrice []struct {
		// TotalFarePrice is the price with taxes.
		TotalFarePrice struct {
			// MilesEquivalentPrice is the miles.
			MilesEquivalentPrice struct {
				// MileCnt is how many miles.
				MileCnt int `json:"mileCnt"`
			} `json:"milesEquivalentPrice"`
			// CurrencyEquivalentPrice is the taxes and fees in cash, nil
			// when not said.
			CurrencyEquivalentPrice *cashPrice `json:"currencyEquivalentPrice"`
		} `json:"totalFarePrice"`
	} `json:"farePrice"`
}

// cashPrice is the cash part of a fare: its taxes and fees.
type cashPrice struct {
	// RoundedCurrencyAmt is the cash rounded to whole units.
	RoundedCurrencyAmt *float64 `json:"roundedCurrencyAmt"`
	// FormattedCurrencyAmt is the exact cash as the site displays it,
	// e.g. "5.60" or "1,234.50".
	FormattedCurrencyAmt string `json:"formattedCurrencyAmt"`
	// CurrencyAmt is the cash.
	CurrencyAmt *float64 `json:"currencyAmt"`
	// CurrencyCode is the ISO 4217 code.
	CurrencyCode string `json:"currencyCode"`
}

// parseAnswer decodes an offer API answer. It returns an error when body is
// not the answer's JSON.
func parseAnswer(body []byte) (answer, error) {
	var a answer
	if err := json.Unmarshal(body, &a); err != nil {
		return answer{}, fmt.Errorf("delta: reading the offer answer: %w", err)
	}
	return a, nil
}

// searchesOffers reports whether a is the answer to a flight search, with
// flights or with an error such as no flights, rather than an empty body.
func (a answer) searchesOffers() bool {
	return (a.Data != nil && a.Data.GqlSearchOffers != nil) ||
		len(a.Errors) > 0
}

// offers turns every priced column of every itinerary in a into an offer
// for q, booked at booking.
func offers(a answer, q award.Query, booking string) []award.Offer {
	out := []award.Offer{}
	if a.Data == nil || a.Data.GqlSearchOffers == nil {
		return out
	}
	g := a.Data.GqlSearchOffers
	var cols []column
	if g.OfferDataList != nil {
		cols = g.OfferDataList.RetailItemDefinitionList
	}
	for _, set := range g.GqlOffersSets {
		if len(set.Trips) == 0 || len(set.Trips[0].FlightSegment) == 0 {
			continue
		}
		t := set.Trips[0]
		times, ok := tripTimes(t)
		if !ok {
			continue
		}
		for i, fo := range set.Offers {
			if i >= len(cols) {
				break
			}
			if o, ok := offer(t, times, cols[i], fo, q, booking); ok {
				out = append(out, o)
			}
		}
	}
	return out
}

// offer builds the offer for trip t, whose segments leave and land at
// times, priced in column col's fare offer fo for q and booked at booking.
// It returns false when fo is sold out, not for sale, unpriced, or has
// fewer seats left than q's passengers.
func offer(
	t trip,
	times []segmentTimes,
	col column,
	fo fareOffer,
	q award.Query,
	booking string,
) (award.Offer, bool) {
	fare, ok := fareOf(fo)
	if !ok {
		return award.Offer{}, false
	}
	pax := max(q.Passengers, 1)
	if fare.AvailableSeatCnt != nil && *fare.AvailableSeatCnt < pax {
		return award.Offer{}, false
	}
	price := fare.FarePrice[0].TotalFarePrice
	cabin := columnCabin(col)
	dominant := ""
	if fo.AdditionalOfferProperties != nil {
		dominant = fo.AdditionalOfferProperties.DominantSegmentBrandID
	}

	segs := make([]award.Segment, len(t.FlightSegment))
	var airlines []string
	var partners []string
	mixed := false
	for i, s := range t.FlightSegment {
		brand, cos := legBrand(fare, s, i)
		sc := cabin
		if brand != "" && brand != dominant {
			sc = brandCabin(brand)
		}
		if sc != cabin {
			mixed = true
		}
		op := s.MarketingCarrier.CarrierCode
		if s.OperatingCarrier != nil && s.OperatingCarrier.CarrierCode != "" {
			op = s.OperatingCarrier.CarrierCode
		}
		if !slices.Contains(airlines, op) {
			airlines = append(airlines, op)
		}
		if op != "DL" && !slices.Contains(partners, op) {
			partners = append(partners, op)
		}
		segs[i] = award.Segment{
			Flight: s.MarketingCarrier.CarrierCode +
				string(s.MarketingCarrier.CarrierNum),
			MarketingCarrier: s.MarketingCarrier.CarrierCode,
			OperatingCarrier: op,
			Origin:           s.OriginAirportCode,
			Destination:      s.DestinationAirportCode,
			Departure:        times[i].departure,
			Arrival:          times[i].arrival,
			DurationMinutes:  minutes(times[i].arrival.Sub(times[i].departure)),
			Aircraft:         aircraft(s),
			Cabin:            sc,
			BookingCode:      cos,
		}
		if i > 0 {
			segs[i-1].LayoverMinutes = minutes(
				times[i].departure.Sub(times[i-1].arrival),
			)
		}
	}

	first, last := segs[0], segs[len(segs)-1]
	duration := minutes(last.Arrival.Sub(first.Departure))
	if t.TotalTripTime != nil {
		duration = t.TotalTripTime.HourCnt*60 + t.TotalTripTime.MinuteCnt
	}
	taxes := taxesOf(price.CurrencyEquivalentPrice)
	points := price.MilesEquivalentPrice.MileCnt
	fareName := col.RetailItemBrandID
	if fareName == "" {
		fareName = dominant
	}

	var notes []string
	if dominant != "" && dominant != fareName {
		notes = append(notes, "Fare brand "+dominant)
	}
	if len(partners) > 0 {
		notes = append(notes, "Partner flights: "+strings.Join(partners, ", "))
	}
	if fare.AvailableSeatCnt != nil && *fare.AvailableSeatCnt <= 3 {
		notes = append(notes,
			fmt.Sprintf("%d seats left", *fare.AvailableSeatCnt))
	}

	return award.Offer{
		Program:         Key,
		Availability:    award.Confirmed,
		Origin:          first.Origin,
		Destination:     last.Destination,
		Departure:       first.Departure,
		Arrival:         last.Arrival,
		DurationMinutes: duration,
		Stops:           len(segs) - 1,
		Airlines:        airlines,
		Cabin:           cabin,
		MixedCabin:      mixed,
		Fare:            fareName,
		Price: award.Price{
			PointsPerPassenger: points,
			PointsTotal:        points * pax,
			TaxesPerPassenger:  taxes,
			TaxesTotal: award.Money{
				Amount:   roundCents(taxes.Amount * float64(pax)),
				Currency: taxes.Currency,
			},
		},
		Seats:      fare.AvailableSeatCnt,
		BookingURL: booking,
		Notes:      notes,
		Segments:   segs,
	}, true
}

// fareOf returns fo's fare, and false when fo is sold out, not for sale or
// has no miles price.
func fareOf(fo fareOffer) (fareInfo, bool) {
	if fo.SoldOut {
		return fareInfo{}, false
	}
	if p := fo.AdditionalOfferProperties; p != nil && p.UnavailableForSale {
		return fareInfo{}, false
	}
	if len(fo.OfferItems) == 0 || len(fo.OfferItems[0].RetailItems) == 0 {
		return fareInfo{}, false
	}
	infos := fo.OfferItems[0].RetailItems[0].RetailItemMetaData.FareInformation
	if len(infos) == 0 || len(infos[0].FarePrice) == 0 {
		return fareInfo{}, false
	}
	f := infos[0]
	if f.FarePrice[0].TotalFarePrice.MilesEquivalentPrice.MileCnt <= 0 {
		return fareInfo{}, false
	}
	return f, true
}

// legBrand returns the brand and fare class fare sells segment s, the i-th
// of its trip, in, matched by segment number or else by position. Both are
// empty when the fare does not say.
func legBrand(fare fareInfo, s segment, i int) (string, string) {
	for _, b := range fare.BrandByFlightLegs {
		num := b.FlightSegmentNum
		if num != "" && num == s.FlightSegmentNum {
			return b.BrandID, b.CosCode
		}
	}
	if i < len(fare.BrandByFlightLegs) &&
		fare.BrandByFlightLegs[i].FlightSegmentNum == "" {
		b := fare.BrandByFlightLegs[i]
		return b.BrandID, b.CosCode
	}
	return "", ""
}

// taxesOf reads the cash part of a price, zero dollars when not given. The
// exact amount is preferred over the one rounded to whole units.
func taxesOf(p *cashPrice) award.Money {
	m := award.Money{Currency: defaultCurrency}
	if p == nil {
		return m
	}
	if p.CurrencyCode != "" {
		m.Currency = p.CurrencyCode
	}
	formatted, formattedErr := strconv.ParseFloat(
		strings.ReplaceAll(p.FormattedCurrencyAmt, ",", ""), 64,
	)
	switch {
	case p.CurrencyAmt != nil:
		m.Amount = roundCents(*p.CurrencyAmt)
	case p.FormattedCurrencyAmt != "" && formattedErr == nil:
		m.Amount = roundCents(formatted)
	case p.RoundedCurrencyAmt != nil:
		m.Amount = roundCents(*p.RoundedCurrencyAmt)
	}
	return m
}

// aircraft names s's aircraft type, empty when the answer does not say.
func aircraft(s segment) string {
	if s.Aircraft == nil {
		return ""
	}
	if s.Aircraft.FleetTypeCode != "" {
		return s.Aircraft.FleetTypeCode
	}
	return s.Aircraft.SubFleetTypeCode
}

// minutes is d in whole minutes.
func minutes(d time.Duration) int {
	return int(d / time.Minute)
}

// roundCents rounds v to two decimals.
func roundCents(v float64) float64 {
	return math.Round(v*100) / 100
}

// priorityCabins are the cabins of Delta's fare columns by rank: Basic,
// Main and Comfort are economy, then First, Premium Select and Delta One.
var priorityCabins = map[string]award.Cabin{
	"1":  award.Economy,
	"2":  award.Economy,
	"5":  award.Economy,
	"8":  award.First,
	"11": award.Premium,
	"14": award.Business,
}

// brandCabins are the cabins of Delta's own brands.
var brandCabins = map[string]award.Cabin{
	"BMAIN":  award.Economy,
	"CMAIN":  award.Economy,
	"CDCP":   award.Economy,
	"CDPS":   award.Premium,
	"CFIRST": award.First,
	"CD1":    award.Business,
}

// Partner brands end in their cabin: CEC and KEEC economy, AFPE premium,
// CBU and CVSUP business.
var (
	businessBrand = regexp.MustCompile(`(D1|BU|UP)$`)
	premiumBrand  = regexp.MustCompile(`(PE|PS)$`)
	firstBrand    = regexp.MustCompile(`(FIRST|FI|FR)$`)
)

// columnCabin is the cabin of col: by its rank where known, else by its
// brand.
func columnCabin(col column) award.Cabin {
	if c, ok := priorityCabins[string(col.RetailItemPriorityText)]; ok {
		return c
	}
	return brandCabin(col.RetailItemBrandID)
}

// brandCabin is the best guess of a brand's cabin from its id alone,
// economy when nothing matches.
func brandCabin(id string) award.Cabin {
	b := strings.ToUpper(id)
	if c, ok := brandCabins[b]; ok {
		return c
	}
	switch {
	case businessBrand.MatchString(b):
		return award.Business
	case premiumBrand.MatchString(b):
		return award.Premium
	case firstBrand.MatchString(b):
		return award.First
	}
	return award.Economy
}
