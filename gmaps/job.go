package gmaps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"uuid"

	"github.com/PuerkitoBio/goquery"
	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
)

const (
	requestMethodGet   = "GET"
	languageQueryParam = "hl"
)

type GmapJobOptions func(*GmapJob)

type GmapJob struct {
	scrapemate.Job

	MaxDepth     int
	LangCode     string
	ExtractEmail bool

	Deduper                 deduper.Deduper
	ExitMonitor             exiter.Exiter
	ExtractExtraReviews     bool
	WriterManagedCompletion bool
	CompletionTracker       CompletionTracker
	ValidatePlaceIdUrl      string
}

func NewGmapJob(
	id, langCode, query string,
	maxDepth int,
	extractEmail bool,
	geoCoordinates string,
	zoom int,
	validatePlaceIdUrl string,
	opts ...GmapJobOptions,
) *GmapJob {
	var mapURL string

	switch {
	case isGoogleMapsURL(query):
		mapURL = sanitizePlaceURL(strings.TrimSpace(query))
	case geoCoordinates != "" && zoom > 0:
		query = url.QueryEscape(query)
		mapURL = fmt.Sprintf("https://www.google.com/maps/search/%s/@%s,%dz", query, strings.ReplaceAll(geoCoordinates, " ", ""), zoom)
	default:
		// Warning: geo and zoom MUST be both set or not
		query = url.QueryEscape(query)
		mapURL = fmt.Sprintf("https://www.google.com/maps/search/%s", query)
	}

	const (
		maxRetries = 3
		prio       = scrapemate.PriorityLow
	)

	if id == "" {
		id = uuid.NewV4().String()
	}

	job := GmapJob{
		Job: scrapemate.Job{
			ID:         id,
			Method:     http.MethodGet,
			URL:        mapURL,
			URLParams:  map[string]string{languageQueryParam: langCode},
			MaxRetries: maxRetries,
			Priority:   prio,
		},
		MaxDepth:           maxDepth,
		LangCode:           langCode,
		ExtractEmail:       extractEmail,
		ValidatePlaceIdUrl: validatePlaceIdUrl,
	}

	for _, opt := range opts {
		opt(&job)
	}

	return &job
}

func WithDeduper(d deduper.Deduper) GmapJobOptions {
	return func(j *GmapJob) {
		j.Deduper = d
	}
}

func WithExitMonitor(e exiter.Exiter) GmapJobOptions {
	return func(j *GmapJob) {
		j.ExitMonitor = e
	}
}

func WithValidatePlaceIdUrl(v string) GmapJobOptions {
	return func(j *GmapJob) {
		j.ValidatePlaceIdUrl = v
	}
}

func WithExtraReviews() GmapJobOptions {
	return func(j *GmapJob) {
		j.ExtractExtraReviews = true
	}
}

func WithWriterManagedCompletion() GmapJobOptions {
	return func(j *GmapJob) {
		j.WriterManagedCompletion = true
	}
}

func WithGmapCompletionTracker(tracker CompletionTracker) GmapJobOptions {
	return func(j *GmapJob) {
		j.CompletionTracker = tracker
	}
}

func (j *GmapJob) UseInResults() bool {
	return false
}

func (j *GmapJob) ProcessOnFetchError() bool {
	return true
}

func (j *GmapJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
	}()

	if resp.Error != nil {
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrSeedCompleted(1)
		}

		return nil, nil, resp.Error
	}

	log := scrapemate.GetLoggerFromContext(ctx)

	doc, ok := resp.Document.(*goquery.Document)
	if !ok {
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrSeedCompleted(1)
		}

		return nil, nil, fmt.Errorf("could not convert to goquery document")
	}

	var next []scrapemate.IJob

	if strings.Contains(resp.URL, "/maps/place/") {
		jopts := []PlaceJobOptions{}
		if j.ExitMonitor != nil {
			jopts = append(jopts, WithPlaceJobExitMonitor(j.ExitMonitor))
		}

		if j.WriterManagedCompletion {
			jopts = append(jopts, WithPlaceJobWriterManagedCompletion())
		}

		placeJob := NewPlaceJob(j.ID, j.LangCode, resp.URL, j.ExtractEmail, j.ExtractExtraReviews, jopts...)

		next = append(next, placeJob)
	} else {
		doc.Find(`div[role=feed] div[jsaction]>a`).Each(func(_ int, s *goquery.Selection) {
			if href := s.AttrOr("href", ""); href != "" {

				if j.ValidatePlaceIdUrl != "" {
					// Make GET request
					resp, err := http.Get(j.ValidatePlaceIdUrl + "?url=" + url.QueryEscape(href))
					if err != nil {
						panic(err)
					}
					defer resp.Body.Close() // Always close response body

					// Read response body
					body, err := io.ReadAll(resp.Body)
					if err != nil {
						panic(err)
					}

					// Body is JSON, check if body has a field message and if it contains "placeid_found"
					var result map[string]interface{}
					if err := json.Unmarshal(body, &result); err != nil {
						panic(err)
					}

					if message, ok := result["message"].(string); ok && strings.Contains(message, "placeid_found") {
						// If found we go to the next item in the loop
						return
					}
				}

				jopts := []PlaceJobOptions{}
				if j.ExitMonitor != nil {
					jopts = append(jopts, WithPlaceJobExitMonitor(j.ExitMonitor))
				}

				if j.WriterManagedCompletion {
					jopts = append(jopts, WithPlaceJobWriterManagedCompletion())
				}

				nextJob := NewPlaceJob(j.ID, j.LangCode, href, j.ExtractEmail, j.ExtractExtraReviews, jopts...)

				if j.Deduper == nil || j.Deduper.AddIfNotExists(ctx, href) {
					next = append(next, nextJob)
				}
			}
		})
	}

	if j.ExitMonitor != nil {
		j.ExitMonitor.IncrPlacesFound(len(next))
		j.ExitMonitor.IncrSeedCompleted(1)
	}

	if j.CompletionTracker != nil {
		_ = j.CompletionTracker.SeedDiscovered(j.ID, len(next))
	}

	log.Info(fmt.Sprintf("%d places found", len(next)))

	return nil, next, nil
}

func (j *GmapJob) BrowserActions(ctx context.Context, page scrapemate.BrowserPage) scrapemate.Response {
	var resp scrapemate.Response

	pageResponse, err := page.Goto(j.GetFullURL(), scrapemate.WaitUntilDOMContentLoaded)
	if err != nil {
		resp.Error = fmt.Errorf("navigation failed: %w", err)
		fmt.Printf("Navigation error: %v\n", err)

		return resp
	}

	clickRejectCookiesIfRequired(page)

	const defaultTimeout = 5 * time.Second

	// Ignore WaitForURL errors — Google Maps may redirect slowly especially via proxy
	_ = page.WaitForURL(page.URL(), defaultTimeout)

	resp.URL = pageResponse.URL
	resp.StatusCode = pageResponse.StatusCode
	resp.Headers = pageResponse.Headers

	// When Google Maps finds only 1 place, it slowly redirects to that place's URL
	// Check for this redirection
	singlePlace := false
	feedSelector := `div[role='feed']`

	// Try multiple selectors for the feed element
	selectors := []string{
		feedSelector,
		".section-layout.section-scrollbox",
		".section-layout.section-scrollbox scrollable-y",
		".m6QErb.DxyBCb.kA9KIf.dS8AEf",
		".m6QErb.DxyBCb.kA9KIf",
		".DxyBCb.kA9KIf",
		".section-scrollbox",
	}

	feedFound := false
	for _, sel := range selectors {
		err := page.WaitForSelector(sel, 700*time.Millisecond)

		if err == nil {
			feedFound = true
			feedSelector = sel
			break
		}
	}

	if !feedFound {
		waitCtx, waitCancel := context.WithTimeout(ctx, time.Second*10)
		defer waitCancel()

		singlePlace = waitUntilURLContains(waitCtx, page, "/maps/place/")
		if !singlePlace {
			// If we're not in a single place view and couldn't find the feed selector,
			// check if we've been redirected to a search results view with a different structure
			fmt.Println("Feed not found, checking for alternative results structure...")

			// Try one last approach - just get the page content regardless
			singlePlace = true
		}

		waitCancel()
	}

	// Handle single place or search results list appropriately
	if singlePlace {
		resp.URL = page.URL()

		var body string

		body, err = page.Content()
		if err != nil {
			resp.Error = err
			return resp
		}

		resp.Body = []byte(body)

		return resp
	}

	// Handle search results with scrolling
	scrollCnt, err := scroll(ctx, page, j.MaxDepth, feedSelector)
	if err != nil {
		fmt.Printf("Scroll error: %v\n", err)
		// Continue to get the content anyway
	}

	fmt.Printf("Scrolled %d times\n", scrollCnt)

	// Get the final page content
	body, err := page.Content()
	if err != nil {
		resp.Error = err
		return resp
	}

	resp.Body = []byte(body)

	return resp
}

func waitUntilURLContains(ctx context.Context, page scrapemate.BrowserPage, s string) bool {
	ticker := time.NewTicker(time.Millisecond * 150)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if strings.Contains(page.URL(), s) {
				return true
			}
		}
	}
}

func clickRejectCookiesIfRequired(page scrapemate.BrowserPage) {
	// Use JavaScript to find and click - faster than multiple locator calls
	_, _ = page.Eval(`() => {
		// Try consent form buttons first
		const consentForm = document.querySelector('form[action*="consent.google"]');
		if (consentForm) {
			const btn = consentForm.querySelector('button, input[type="submit"]');
			if (btn) {
				btn.click();
				return true;
			}
		}
		// Try reject/decline buttons
		const buttons = document.querySelectorAll('button, input[type="submit"]');
		for (const btn of buttons) {
			const text = (btn.textContent || btn.value || '').toLowerCase();
			if (text.includes('reject') || text.includes('decline') || text.includes('ablehnen')) {
				btn.click();
				return true;
			}
		}
		return false;
	}`)
}

func scroll(ctx context.Context,
	page scrapemate.BrowserPage,
	maxDepth int,
	scrollSelector string,
) (int, error) {
	// First, check if the selector exists at all
	hasElement, err := page.Eval(fmt.Sprintf(`() => {
		const selectors = [
			"%s",
			"div[role='feed']",
			".section-layout.section-scrollbox",
			".section-layout.section-scrollbox scrollable-y",
			".m6QErb.DxyBCb.kA9KIf.dS8AEf",
			".m6QErb.DxyBCb.kA9KIf",
			".DxyBCb.kA9KIf",
			".section-scrollbox",
			".Yr7JMd.fontTitleLarge"
		];
		
		for (const selector of selectors) {
			const el = document.querySelector(selector);
			if (el) {
				console.log("Found scrollable element: " + selector);
				return true;
			}
		}
		
		console.error("No scrollable element found with any of the selectors");
		return false;
	}`, scrollSelector))

	if err != nil {
		fmt.Printf("Error checking for scrollable elements: %v\n", err)
	} else if hasElement.(bool) == false {
		fmt.Println("No scrollable elements found, will try to scroll the document body")

		// If no elements found, just scroll the document and return
		for i := 0; i < maxDepth; i++ {
			_, err := page.Eval(`() => {
				window.scrollBy(0, 500);
				return document.body.scrollHeight;
			}`)

			if err != nil {
				return i, fmt.Errorf("failed to scroll document: %w", err)
			}

			// Wait between scrolls
			page.WaitForTimeout(500)
		}

		return maxDepth, nil
	}

	// Continue with the normal scrolling if we found elements
	expr := `async () => {
		try {
			// Try multiple potential selectors in case the UI structure has changed
			const selectors = [
				"` + scrollSelector + `",
				"div[role='feed']",
				".section-layout.section-scrollbox",
				".section-layout.section-scrollbox scrollable-y",
				".m6QErb.DxyBCb.kA9KIf.dS8AEf",
				".m6QErb.DxyBCb.kA9KIf",
				".DxyBCb.kA9KIf",
				".section-scrollbox",
				".Yr7JMd.fontTitleLarge"
			];
			
			let el = null;
			for (const selector of selectors) {
				el = document.querySelector(selector);
				if (el) {
					console.log("Using selector for scrolling: " + selector);
					break;
				}
			}
			
			// If no scrollable element is found, try the document body or return 0
			if (!el) {
				console.warn("No scrollable element found for scrolling, using document.body");
				el = document.body;
				
				if (!el) {
					console.error("No scrollable element found, not even document.body");
					return 0;
				}
			}
			
			// Log the scroll properties for debugging
			console.log("Element properties before scroll - scrollHeight: " + 
				(el.scrollHeight || "undefined") + 
				", scrollTop: " + (el.scrollTop || "undefined") + 
				", clientHeight: " + (el.clientHeight || "undefined"));
			
			// Safely attempt to scroll
			try {
				const scrollHeight = el.scrollHeight || 0;
				if (typeof el.scrollTop !== 'undefined') {
					el.scrollTop = scrollHeight;
					console.log("Scrolled element to: " + el.scrollTop);
				} else {
					// Fallback to window scrolling
					window.scrollTo(0, document.body.scrollHeight);
					console.log("Used window.scrollTo fallback");
				}
				
				return new Promise((resolve) => {
					setTimeout(() => {
						const newScrollHeight = el.scrollHeight || 0;
						console.log("New scroll height: " + newScrollHeight);
						resolve(newScrollHeight);
					}, %d);
				});
			} catch (e) {
				console.error("Error during scroll:", e);
				// Try window.scrollBy as a fallback
				window.scrollBy(0, 500);
				console.log("Used window.scrollBy fallback due to error");
				return 0;
			}
		} catch (outerError) {
			console.error("Outer error in scroll function:", outerError);
			return 0;
		}
	}`

	var currentScrollHeight int
	// Scroll to the bottom of the page.
	waitTime := 100.
	cnt := 0

	const (
		timeout  = 500
		maxWait2 = 2000
	)

	for i := 0; i < maxDepth; i++ {
		cnt++
		waitTime2 := timeout * cnt

		if waitTime2 > timeout {
			waitTime2 = maxWait2
		}

		// Scroll to the bottom of the page.
		scrollHeight, err := page.Eval(fmt.Sprintf(expr, waitTime2))
		if err != nil {
			fmt.Printf("Scroll error on iteration %d: %v\n", i, err)

			// Try a simple fallback
			_, fallbackErr := page.Eval(`() => {
				window.scrollBy(0, 500);
				return true;
			}`)

			if fallbackErr != nil {
				return cnt, err // Return the original error if fallback also fails
			}

			// Wait and continue
			page.WaitForTimeout(500)
			continue
		}

		// Handle both int and float64 because browser-evaluated numbers may arrive as either type.
		var height int

		switch v := scrollHeight.(type) {
		case int:
			height = v
		case float64:
			height = int(v)
		default:
			return cnt, fmt.Errorf("scrollHeight is not a number, got %T", scrollHeight)
		}

		if height == 0 || height == currentScrollHeight {
			// If height is 0 or hasn't changed, try one more approach with window.scrollBy
			_, byErr := page.Eval(`() => {
				window.scrollBy(0, 500);
				console.log("Used window.scrollBy because height is unchanged or zero");
				return true;
			}`)

			if byErr != nil {
				// If even this fails, break the loop
				break
			}
		}

		currentScrollHeight = height

		select {
		case <-ctx.Done():
			return currentScrollHeight, nil
		default:
		}

		waitTime *= 1.5

		if waitTime > maxWait2 {
			waitTime = maxWait2
		}

		page.WaitForTimeout(time.Duration(waitTime) * time.Millisecond)
	}

	return cnt, nil
}

func isGoogleMapsURL(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil {
			return false
		}

		host := strings.ToLower(u.Hostname())
		if host == "maps.app.goo.gl" {
			return true
		}

		return (host == "google.com" || strings.HasSuffix(host, ".google.com")) &&
			(strings.Contains(u.EscapedPath(), "/maps") || strings.Contains(u.Path, "/maps"))
	}

	if strings.HasPrefix(s, "maps.app.goo.gl") {
		return true
	}

	return false
}
