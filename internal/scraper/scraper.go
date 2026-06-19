package scraper

import (
	"crypto/md5"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/mbcaira/scout/internal/store"
)

type Target struct {
	Name       string
	CareersURL string
	Keywords   []string
	ATS        string
	AutoApply  bool
}

type Config struct {
	Targets         []Target
	ExcludeKeywords []string
	Locations       []string
}

type Scraper struct {
	cfg Config
}

func New(cfg Config) *Scraper {
	return &Scraper{cfg: cfg}
}

func (s *Scraper) Run() ([]store.Job, error) {
	browser := rod.New()
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("launch browser: %w", err)
	}
	defer browser.Close()

	// phase 1: scrape listing pages concurrently
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		jobs []store.Job
		errs []error
	)

	for _, target := range s.cfg.Targets {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			found, err := s.scrapeTarget(browser, t)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", t.Name, err))
				return
			}
			jobs = append(jobs, found...)
		}(target)
	}

	wg.Wait()

	for _, e := range errs {
		fmt.Printf("scrape error: %v\n", e)
	}

	// phase 2: fetch descriptions from individual job pages (max 3 concurrent)
	fmt.Printf("fetching descriptions for %d jobs...\n", len(jobs))
	sem := make(chan struct{}, 3)
	for i := range jobs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			fmt.Printf("  fetching: %s\n", jobs[idx].Title)
			desc, err := s.fetchDescription(browser, jobs[idx].URL)
			if err != nil {
				fmt.Printf("  description error for %s: %v\n", jobs[idx].Title, err)
				return
			}
			mu.Lock()
			jobs[idx].Description = desc
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	return jobs, nil
}

func (s *Scraper) fetchDescription(browser *rod.Browser, url string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("empty URL")
	}

	// resolve relative URLs — HRT uses relative paths
	if strings.HasPrefix(url, "/") {
		url = "https://www.hudsonrivertrading.com" + url
	}

	page, err := browser.Page(proto.TargetCreateTarget{URL: url})
	if err != nil {
		return "", err
	}
	defer page.Close()

	// WaitDOMContentLoaded is faster and sufficient for static job descriptions;
	// WaitLoad hangs on pages with unresolved third-party scripts
	if err := page.Timeout(10 * time.Second).WaitLoad(); err != nil {
		return "", fmt.Errorf("timeout waiting for page: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	html, err := page.HTML()
	if err != nil {
		return "", err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", err
	}

	// remove noise
	doc.Find("script, style, nav, header, footer, [role='navigation']").Remove()

	// try specific content containers first
	containers := []string{
		"#content", ".content", "main", "article",
		".job-description", ".description", "#job-description",
		".posting-description", "[class*='description']",
	}

	for _, sel := range containers {
		text := strings.TrimSpace(doc.Find(sel).Text())
		if len(text) > 100 {
			return truncate(text, 3000), nil
		}
	}

	// fall back to body text
	text := strings.TrimSpace(doc.Find("body").Text())
	return truncate(text, 3000), nil
}

func (s *Scraper) scrapeTarget(browser *rod.Browser, t Target) ([]store.Job, error) {
	switch t.ATS {
	case "greenhouse":
		return s.scrapeGreenhouse(browser, t)
	default:
		return s.scrapeGeneric(browser, t)
	}
}

func (s *Scraper) scrapeGreenhouse(browser *rod.Browser, t Target) ([]store.Job, error) {
	page, err := browser.Page(proto.TargetCreateTarget{URL: t.CareersURL})
	if err != nil {
		return nil, err
	}
	defer page.Close()

	if err := page.Timeout(15 * time.Second).WaitLoad(); err != nil {
		return nil, fmt.Errorf("timeout: %w", err)
	}
	time.Sleep(2 * time.Second)

	html, err := page.HTML()
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}

	var jobs []store.Job
	doc.Find(".opening").Each(func(_ int, sel *goquery.Selection) {
		title := strings.TrimSpace(sel.Find("a").Text())
		href, _ := sel.Find("a").Attr("href")
		location := strings.TrimSpace(sel.Find(".location").Text())

		if title == "" || href == "" {
			return
		}
		if !s.matches(title, location, t.Keywords) {
			return
		}

		jobs = append(jobs, store.Job{
			ID:        jobID(t.Name, href),
			Company:   t.Name,
			Title:     title,
			URL:       href,
			Location:  location,
			AutoApply: t.AutoApply,
			Status:    "pending",
			SeenAt:    time.Now(),
		})
	})

	return jobs, nil
}

func (s *Scraper) scrapeGeneric(browser *rod.Browser, t Target) ([]store.Job, error) {
	page, err := browser.Page(proto.TargetCreateTarget{URL: t.CareersURL})
	if err != nil {
		return nil, err
	}
	defer page.Close()

	if err := page.Timeout(15 * time.Second).WaitLoad(); err != nil {
		return nil, fmt.Errorf("timeout: %w", err)
	}
	time.Sleep(2 * time.Second)

	html, err := page.HTML()
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}

	var jobs []store.Job
	seen := make(map[string]bool)

	doc.Find("a").Each(func(_ int, sel *goquery.Selection) {
		title := strings.TrimSpace(sel.Text())
		href, exists := sel.Attr("href")
		if !exists || href == "" || title == "" {
			return
		}

		if len(strings.Fields(title)) < 2 {
			return
		}

		lowerHref := strings.ToLower(href)
		if !strings.Contains(lowerHref, "job") &&
			!strings.Contains(lowerHref, "career") &&
			!strings.Contains(lowerHref, "position") &&
			!strings.Contains(lowerHref, "opening") &&
			!strings.Contains(lowerHref, "role") {
			return
		}

		if seen[href] {
			return
		}
		seen[href] = true

		if !s.matches(title, "", t.Keywords) {
			return
		}

		jobs = append(jobs, store.Job{
			ID:        jobID(t.Name, href),
			Company:   t.Name,
			Title:     title,
			URL:       href,
			AutoApply: t.AutoApply,
			Status:    "pending",
			SeenAt:    time.Now(),
		})
	})

	return jobs, nil
}

func (s *Scraper) matches(title, location string, keywords []string) bool {
	lower := strings.ToLower(title + " " + location)

	for _, kw := range s.cfg.ExcludeKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return false
		}
	}

	if len(keywords) == 0 {
		return true
	}

	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return true
		}
	}

	return false
}

func truncate(s string, max int) string {
	// collapse whitespace
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func jobID(company, url string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(company+url)))
}
