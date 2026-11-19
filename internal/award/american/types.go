package american

// state is the part of the results page's Angular transfer state the
// program reads.
type state struct {
	// SearchData is the search answer, nil when the page has none.
	SearchData *searchData `json:"SearchData"`
}

// searchData is the search answer: the itineraries, or an error.
type searchData struct {
	// ItineraryResult is the itineraries found, nil on an error.
	ItineraryResult *itineraryResult `json:"itineraryResult"`
	// Error is the site's error, nil when the search succeeded.
	Error *siteError `json:"error"`
}

// siteError is an error the site reports instead of results.
type siteError struct {
	// Code is the site's error code, e.g. "858" for no flights.
	Code string `json:"code"`
	// Message is the site's description of it.
	Message string `json:"message"`
}

// itineraryResult is the itineraries of one search.
type itineraryResult struct {
	// Slices are the itineraries, one per routing.
	Slices []slice `json:"slices"`
	// Error is the site's error code, empty when there is none.
	Error string `json:"error"`
}

// slice is one itinerary with its prices in every product.
type slice struct {
	// DurationInMinutes is the door-to-door time.
	DurationInMinutes int `json:"durationInMinutes"`
	// CarrierNames are the names of the airlines flying it.
	CarrierNames []string `json:"carrierNames"`
	// Segments are its flights, in order.
	Segments []segment `json:"segments"`
	// PricingDetail is its price in each product.
	PricingDetail []pricing `json:"pricingDetail"`
}

// segment is one flight number of an itinerary, flown as one or more legs.
type segment struct {
	// Flight is the marketing carrier and number.
	Flight flight `json:"flight"`
	// DepartureDateTime is when it leaves, with offset.
	DepartureDateTime string `json:"departureDateTime"`
	// ArrivalDateTime is when it lands, with offset.
	ArrivalDateTime string `json:"arrivalDateTime"`
	// Origin is where it leaves from.
	Origin airport `json:"origin"`
	// Destination is where it lands.
	Destination airport `json:"destination"`
	// Legs are its takeoffs and landings.
	Legs []leg `json:"legs"`
}

// flight is a marketing carrier and flight number.
type flight struct {
	// CarrierCode is the airline's IATA code.
	CarrierCode string `json:"carrierCode"`
	// FlightNumber is the number without the carrier.
	FlightNumber string `json:"flightNumber"`
}

// airport is an airport the site names.
type airport struct {
	// Code is its IATA code.
	Code string `json:"code"`
}

// leg is one takeoff and landing.
type leg struct {
	// DepartureDateTime is when it leaves, with offset.
	DepartureDateTime string `json:"departureDateTime"`
	// ArrivalDateTime is when it lands, with offset.
	ArrivalDateTime string `json:"arrivalDateTime"`
	// DurationInMinutes is the time in the air.
	DurationInMinutes int `json:"durationInMinutes"`
	// Origin is where it leaves from.
	Origin airport `json:"origin"`
	// Destination is where it lands.
	Destination airport `json:"destination"`
	// Aircraft is the aircraft flying it.
	Aircraft aircraft `json:"aircraft"`
	// OperationalDisclosure names the operating airline, e.g. "Republic
	// Airways as American Eagle".
	OperationalDisclosure string `json:"operationalDisclosure"`
	// ProductDetails are its cabin and booking code in each product.
	ProductDetails []productDetail `json:"productDetails"`
}

// aircraft is an aircraft type.
type aircraft struct {
	// Name is its full name, e.g. "Boeing 777-200/200ER Passenger".
	Name string `json:"name"`
}

// productDetail is how a leg is flown when sold in one product.
type productDetail struct {
	// ProductType is the product, e.g. "BUSINESS".
	ProductType string `json:"productType"`
	// CabinType is the cabin actually flown, e.g. "COACH".
	CabinType string `json:"cabinType"`
	// BookingCode is the fare class letter.
	BookingCode string `json:"bookingCode"`
}

// pricing is an itinerary's price in one product.
type pricing struct {
	// ProductType is the product, e.g. "COACH" or "PREMIUM_ECONOMY".
	ProductType string `json:"productType"`
	// ProductAvailable is set when award space is sold in it.
	ProductAvailable bool `json:"productAvailable"`
	// PerPassengerAwardPoints is the miles price for one passenger.
	PerPassengerAwardPoints int `json:"perPassengerAwardPoints"`
	// PerPassengerTaxesAndFees is the cash part for one passenger.
	PerPassengerTaxesAndFees amount `json:"perPassengerTaxesAndFees"`
	// AllPassengerTaxesAndFees is the cash part for every passenger.
	AllPassengerTaxesAndFees amount `json:"allPassengerTaxesAndFees"`
	// SeatsRemaining is the seats left, 0 when the site does not say.
	SeatsRemaining int `json:"seatsRemaining"`
	// Fares are the fares it is built from.
	Fares []fare `json:"fares"`
}

// fare is one fare of a price.
type fare struct {
	// BrandInfo names the fare's brand.
	BrandInfo struct {
		// BrandCode is the brand, e.g. "MAIN" or "FBUS".
		BrandCode string `json:"brandCode"`
	} `json:"brandInfo"`
}

// amount is money as the site gives it.
type amount struct {
	// Amount is in the currency's major unit.
	Amount float64 `json:"amount"`
	// Currency is the ISO 4217 code.
	Currency string `json:"currency"`
}
