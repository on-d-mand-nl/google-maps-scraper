package gmaps

import (
	"encoding/json"
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func searchPayload(t *testing.T, businesses ...[]any) []byte {
	t.Helper()
	items := []any{nil}
	for _, business := range businesses {
		item := make([]any, 15)
		item[14] = business
		items = append(items, item)
	}
	data, err := json.Marshal([]any{[]any{nil, items}})
	require.NoError(t, err)
	return data
}

func TestFastResultsUserExamples(t *testing.T) {
	raw, err := os.ReadFile("testdata/fast_results_examples.json")
	require.NoError(t, err)
	var originals []Entry
	require.NoError(t, json.Unmarshal(raw, &originals))
	var businesses [][]any
	for _, entry := range originals {
		businesses = append(businesses, entry.Raw)
	}
	entries, err := ParseSearchResults(searchPayload(t, businesses...))
	require.NoError(t, err)
	require.Len(t, entries, 10)
	descriptions, reservations := 0, 0
	for i, entry := range entries {
		old := originals[i]
		require.Equal(t, old.Title, entry.Title)
		require.Equal(t, old.Address, entry.Address)
		require.Equal(t, old.OpenHours, entry.OpenHours)
		require.Equal(t, old.ReviewCount, entry.ReviewCount)
		require.Equal(t, old.ReviewRating, entry.ReviewRating)
		require.Equal(t, old.Latitude, entry.Latitude)
		require.Equal(t, old.Longtitude, entry.Longtitude)
		require.Equal(t, old.Categories[0], entry.Category)
		require.NotEmpty(t, entry.PlaceID)
		require.NotEmpty(t, entry.Cid)
		require.NotEmpty(t, entry.ReviewsLink)
		require.Equal(t, "Amsterdam", entry.CompleteAddress.City)
		require.Equal(t, "NL", entry.CompleteAddress.Country)
		require.NotEmpty(t, entry.CompleteAddress.PostalCode)
		require.Empty(t, entry.Status, "opening-hours labels and descriptions are not business closure status")
		require.NotEmpty(t, entry.OpeningStatus)
		link, err := url.Parse(entry.Link)
		require.NoError(t, err)
		require.Equal(t, entry.PlaceID, link.Query().Get("query_place_id"))
		if entry.Description != "" {
			descriptions++
		}
		if len(entry.Reservations) > 0 {
			reservations++
		}
		if i > 0 {
			require.NotEmpty(t, entry.About)
		}
	}
	require.Equal(t, 6, descriptions)
	require.Equal(t, 5, reservations)
	require.Equal(t, "ChIJP-qfrgYJxkcRCVdCvC9uhP0", entries[0].PlaceID)
	require.Equal(t, "18267847139822556937", entries[0].Cid) // exceeds signed int64
	require.Equal(t, "+31207608877", entries[0].Phone)
	require.Equal(t, "+31205550255", entries[1].Phone)
	require.Equal(t, "Closed · Opens 12\u202fpm", entries[1].OpeningStatus)
	require.Equal(t, "Herengracht 339", entries[1].CompleteAddress.Street)
	require.Contains(t, entries[1].Description, "tasting menus")
	require.True(t, entries[1].About[0].Options[0].Enabled)
	require.False(t, entries[1].About[0].Options[1].Enabled)
	require.Equal(t, "Google Maps", entries[1].Reservations[len(entries[1].Reservations)-1].Source)
	require.Empty(t, entries[1].OrderOnline) // reservations must not become delivery links
	require.Empty(t, entries[0].Description)
	require.Empty(t, entries[0].About)
}

func TestFastResultsSparseAndMalformedFields(t *testing.T) {
	business := make([]any, 204)
	business[10] = "not-a-data-id"
	business[11] = "Sparse place"
	business[13] = []any{"Cafe"}
	business[78] = 42
	business[88] = []any{"A pleasant cafe"}
	business[100] = "invalid"
	business[178] = []any{[]any{"020 123 4567", []any{"invalid"}}}
	business[183] = []any{nil, "invalid"}
	entries, err := ParseSearchResults(searchPayload(t, nil, business))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	entry := entries[0]
	require.Empty(t, entry.PlaceID)
	require.Empty(t, entry.Cid)
	require.Empty(t, entry.Link)
	require.Empty(t, entry.Status)
	require.Empty(t, entry.OpeningStatus)
	require.Empty(t, entry.About)
	require.Equal(t, "0201234567", entry.Phone)
	business[4] = []any{nil, nil, nil, []any{"https://search.google.com/local/reviews?placeid=example"}}
	business[88] = []any{"CLOSED"}
	entries, err = ParseSearchResults(searchPayload(t, business))
	require.NoError(t, err)
	require.Equal(t, "example", entries[0].PlaceID)
	require.Equal(t, "CLOSED", entries[0].Status)
}
