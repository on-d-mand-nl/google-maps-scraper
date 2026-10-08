package gmaps

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	olc "github.com/google/open-location-code/go"
)

func ParseSearchResults(raw []byte) ([]*Entry, error) {
	var data []any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("empty JSON data")
	}

	container, ok := data[0].([]any)
	if !ok || len(container) == 0 {
		return nil, fmt.Errorf("invalid business list structure")
	}

	items := getNthElementAndCast[[]any](container, 1)
	if len(items) < 2 {
		return nil, fmt.Errorf("empty business list")
	}

	entries := make([]*Entry, 0, len(items)-1)

	for i := 1; i < len(items); i++ {
		arr, ok := items[i].([]any)
		if !ok {
			continue
		}

		business := getNthElementAndCast[[]any](arr, 14)

		if len(business) == 0 {
			continue
		}

		var entry Entry

		entry.ID = getNthElementAndCast[string](business, 0)
		entry.Title = getNthElementAndCast[string](business, 11)
		entry.Categories = toStringSlice(getNthElementAndCast[[]any](business, 13))
		entry.WebSite = getNthElementAndCast[string](business, 7, 0)

		entry.ReviewRating = getNthElementAndCast[float64](business, 4, 7)
		entry.ReviewCount = int(getNthElementAndCast[float64](business, 4, 8))

		fullAddress := getNthElementAndCast[[]any](business, 2)

		entry.Address = func() string {
			sb := strings.Builder{}

			for i, part := range fullAddress {
				if i > 0 {
					sb.WriteString(", ")
				}

				fmt.Fprintf(&sb, "%v", part)
			}

			return sb.String()
		}()

		entry.Latitude = getNthElementAndCast[float64](business, 9, 2)
		entry.Longtitude = getNthElementAndCast[float64](business, 9, 3)
		entry.Phone = strings.ReplaceAll(getNthElementAndCast[string](business, 178, 0, 0), " ", "")
		entry.OpenHours = getHours(business)
		entry.Status = getNthElementAndCast[string](business, 34, 4, 4)
		entry.Timezone = getNthElementAndCast[string](business, 30)
		entry.DataID = getNthElementAndCast[string](business, 10)

		entry.PlusCode = olc.Encode(entry.Latitude, entry.Longtitude, 10)

		populateFastDetails(&entry, business)

		entry.Raw = business

		entries = append(entries, &entry)
	}

	return entries, nil
}

func toStringSlice(arr []any) []string {
	ans := make([]string, 0, len(arr))
	for _, v := range arr {
		ans = append(ans, fmt.Sprintf("%v", v))
	}

	return ans
}

// populateFastDetails maps additional fields already present in a search response.
// Google array offsets are shared with the detail response where applicable.
func populateFastDetails(entry *Entry, business []any) {
	if len(entry.Categories) > 0 {
		entry.Category = entry.Categories[0]
	}
	entry.PlaceID = getNthElementAndCast[string](business, 78)
	entry.ReviewsLink = getNthElementAndCast[string](business, 4, 3, 0)
	if entry.PlaceID == "" {
		if reviewsURL, err := url.Parse(entry.ReviewsLink); err == nil {
			entry.PlaceID = reviewsURL.Query().Get("placeid")
		}
	}
	if _, hexID, ok := strings.Cut(entry.DataID, ":"); ok {
		if cid, err := strconv.ParseUint(strings.TrimPrefix(hexID, "0x"), 16, 64); err == nil {
			entry.Cid = strconv.FormatUint(cid, 10)
		}
	}
	if entry.PlaceID != "" {
		entry.Link = "https://www.google.com/maps/search/?" + url.Values{"api": {"1"}, "query": {entry.Title}, "query_place_id": {entry.PlaceID}}.Encode()
	} else if entry.Cid != "" {
		entry.Link = "https://www.google.com/maps?cid=" + entry.Cid
	}
	entry.Description = getNthElementAndCast[string](business, 32, 1, 1)
	entry.OpeningStatus = getNthElementAndCast[string](business, 203, 1, 4, 0)
	// [88][0] can be a business description: only accept known closure enums.
	if entry.Status == "" {
		switch status := getNthElementAndCast[string](business, 88, 0); status {
		case "CLOSED", "PERMANENTLY_CLOSED", "TEMPORARILY_CLOSED":
			entry.Status = status
		}
	}
	entry.CompleteAddress = Address{
		Borough:    getNthElementAndCast[string](business, 183, 1, 0),
		Street:     getNthElementAndCast[string](business, 183, 1, 1),
		City:       getNthElementAndCast[string](business, 183, 1, 3),
		PostalCode: getNthElementAndCast[string](business, 183, 1, 4),
		State:      getNthElementAndCast[string](business, 183, 1, 5),
		Country:    getNthElementAndCast[string](business, 183, 1, 6),
	}
	entry.WebSite = extractActualURL(entry.WebSite)
	// Prefer the explicitly supplied international variant; never infer a country code.
	for _, value := range getNthElementAndCast[[]any](business, 178, 0, 1) {
		variant, ok := value.([]any)
		if !ok {
			continue
		}
		phone := getNthElementAndCast[string](variant, 0)
		if strings.HasPrefix(phone, "+") {
			entry.Phone = phone
			break
		}
	}
	entry.Phone = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' || r == '(' || r == ')' {
			return -1
		}
		return r
	}, entry.Phone)
	populateAbout(entry, business)
	entry.Reservations = getLinkSource(getLinkSourceParams{arr: getNthElementAndCast[[]any](business, 46), link: []int{0}, source: []int{1}})
	for _, value := range getNthElementAndCast[[]any](business, 75, 0) {
		action, ok := value.([]any)
		if !ok {
			continue
		}
		// Action type 1 is a reservation. Do not mislabel its provider links as ordering links.
		if getNthElementAndCast[float64](action, 0) != 1 {
			continue
		}
		link := getNthElementAndCast[string](action, 5, 1, 2, 0)
		if link != "" {
			entry.Reservations = append(entry.Reservations, LinkSource{Link: link, Source: "Google Maps"})
		}
	}
}
