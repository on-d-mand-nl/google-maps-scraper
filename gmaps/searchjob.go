package gmaps

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"uuid"

	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/scrapemate"
)

// Earth radius in meters (WGS84)
const earthRadius = 6378137.0

type SearchJobOptions func(*SearchJob)

type MapLocation struct {
	Lat     float64
	Lon     float64
	ZoomLvl float64
	Radius  float64
}

type MapSearchParams struct {
	Location  MapLocation
	Query     string
	ViewportW int
	ViewportH int
	Hl        string
}

type SearchJob struct {
	scrapemate.Job

	params                  *MapSearchParams
	ExitMonitor             exiter.Exiter
	WriterManagedCompletion bool
}

func NewSearchJob(params *MapSearchParams, opts ...SearchJobOptions) *SearchJob {
	const (
		defaultPrio       = scrapemate.PriorityMedium
		defaultMaxRetries = 3
		baseURL           = "https://maps.google.com/search"
	)

	job := SearchJob{
		Job: scrapemate.Job{
			ID:         uuid.NewV4().String(),
			Method:     http.MethodGet,
			URL:        baseURL,
			URLParams:  buildGoogleMapsParams(params),
			MaxRetries: defaultMaxRetries,
			Priority:   defaultPrio,
		},
	}

	job.params = params

	for _, opt := range opts {
		opt(&job)
	}

	return &job
}

func WithSearchJobExitMonitor(exitMonitor exiter.Exiter) SearchJobOptions {
	return func(j *SearchJob) {
		j.ExitMonitor = exitMonitor
	}
}

func WithSearchJobWriterManagedCompletion() SearchJobOptions {
	return func(j *SearchJob) {
		j.WriterManagedCompletion = true
	}
}

func (j *SearchJob) ProcessOnFetchError() bool {
	return true
}

func (j *SearchJob) Process(_ context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
		resp.Meta = nil
	}()

	if resp.Error != nil {
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrSeedCompleted(1)
		}

		return nil, nil, resp.Error
	}

	body := removeFirstLine(resp.Body)
	if len(body) == 0 {
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrSeedCompleted(1)
		}
		return nil, nil, fmt.Errorf("empty response body")
	}

	entries, err := ParseSearchResults(body)
	if err != nil {
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrSeedCompleted(1)
		}
		return nil, nil, fmt.Errorf("failed to parse search results: %w", err)
	}

	entries = filterAndSortEntriesWithinRadius(entries,
		j.params.Location.Lat,
		j.params.Location.Lon,
		j.params.Location.Radius,
	)

	if j.ExitMonitor != nil {
		j.ExitMonitor.IncrPlacesFound(len(entries))
		j.ExitMonitor.IncrSeedCompleted(1)

		if !j.WriterManagedCompletion {
			j.ExitMonitor.IncrPlacesCompleted(len(entries))
		}
	}

	return entries, nil, nil
}

func removeFirstLine(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	index := bytes.IndexByte(data, '\n')
	if index == -1 {
		return []byte{}
	}

	return data[index+1:]
}

// Altitude calculates the approximate altitude (in meters)
// for a given viewport size, latitude and zoom level.
// widthPx, heightPx: viewport dimensions in pixels
// lat: latitude in degrees
// zoom: Google Maps zoom level (integer or float)
func Altitude(widthPx int, heightPx int, lat float64, zoom float64) float64 {
	// Convert latitude to radians
	latRad := lat * math.Pi / 180.0

	// Resolution at equator (meters per pixel)
	resEquator := (2 * math.Pi * earthRadius) / (256.0 * math.Pow(2, float64(zoom)))

	// Correct resolution for latitude
	resLat := resEquator * math.Cos(latRad)

	// Width of map view in meters
	viewWidth := float64(widthPx) * resLat

	// Approximate altitude as half the viewport width
	altitude := viewWidth / 2.0

	return altitude
}

func buildGoogleMapsParams(params *MapSearchParams) map[string]string {
	params.ViewportH = 1000
	params.ViewportW = 1000

	ans := map[string]string{
		"tbm":              "map",
		"authuser":         "0",
		languageQueryParam: params.Hl,
		"q":                params.Query,
	}

	alt := Altitude(params.ViewportW, params.ViewportH, params.Location.Lat, params.Location.ZoomLvl)

	pb := fmt.Sprintf("!4m12!1m3!1d%f!2d%.4f!3d%.4f!2m3!1f0!2f0!3f0!3m2!1i%d!2i%d!4f%.1f!7i20!8i0"+
		"!10b1!12m22!1m3!18b1!30b1!34e1!2m3!5m1!6e2!20e3!4b0!10b1!12b1!13b1!16b1!17m1!3e1!20m3!5e2!6b1!14b1!46m1!1b0"+
		"!96b1!19m4!2m3!1i360!2i120!4i8",
		alt,
		params.Location.Lon,
		params.Location.Lat,
		params.ViewportW,
		params.ViewportH,
		params.Location.ZoomLvl,
	)

	ans["pb"] = pb

	return ans
}
