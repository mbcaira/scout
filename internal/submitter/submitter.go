package submitter

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/mbcaira/scout/internal/generator"
	"github.com/mbcaira/scout/internal/store"
)

type Candidate struct {
	FirstName  string
	LastName   string
	Email      string
	Phone      string
	Location   string
	ResumePath string
	LinkedIn   string
	GitHub     string
}

type Submitter struct {
	candidate Candidate
}

func New(c Candidate) *Submitter {
	return &Submitter{candidate: c}
}

func (s *Submitter) Submit(job store.Job, pkg generator.Package) error {
	url := launcher.New().Headless(true).MustLaunch()
	browser := rod.New().ControlURL(url)
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}
	defer browser.Close()

	page, err := browser.Page(proto.TargetCreateTarget{URL: job.URL})
	if err != nil {
		return fmt.Errorf("open job page: %w", err)
	}
	defer page.Close()

	if err := page.Timeout(15 * time.Second).WaitLoad(); err != nil {
		return fmt.Errorf("wait for page load: %w", err)
	}

	if err := s.navigateToForm(page); err != nil {
		return fmt.Errorf("navigate to form: %w", err)
	}

	if err := page.Timeout(15 * time.Second).WaitLoad(); err != nil {
		return fmt.Errorf("wait for form load: %w", err)
	}
	time.Sleep(time.Second)

	if err := s.fillForm(page, pkg); err != nil {
		return fmt.Errorf("fill form: %w", err)
	}

	if err := s.submit(page); err != nil {
		return fmt.Errorf("submit: %w", err)
	}

	fmt.Printf("applied: %s at %s\n", job.Title, job.Company)
	return nil
}

func (s *Submitter) navigateToForm(page *rod.Page) error {
	applySelectors := []string{
		"a[href*='job_applications/new']",
		"a[href*='/apply']",
		".apply-button a",
		"#apply_button",
		"a.apply",
	}

	for _, sel := range applySelectors {
		el, err := page.Element(sel)
		if err != nil {
			continue
		}
		if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
			continue
		}
		return nil
	}

	return nil
}

func (s *Submitter) fillForm(page *rod.Page, pkg generator.Package) error {
	c := s.candidate

	fields := []struct {
		selectors []string
		value     string
		isFile    bool
	}{
		{
			selectors: []string{"#first_name", "input[name='job_application[first_name]']", "input[placeholder*='First']"},
			value:     c.FirstName,
		},
		{
			selectors: []string{"#last_name", "input[name='job_application[last_name]']", "input[placeholder*='Last']"},
			value:     c.LastName,
		},
		{
			selectors: []string{"#email", "input[name='job_application[email]']", "input[type='email']"},
			value:     c.Email,
		},
		{
			selectors: []string{"#phone", "input[name='job_application[phone]']", "input[type='tel']"},
			value:     c.Phone,
		},
		{
			selectors: []string{"input[name='job_application[location]']", "#location", "input[placeholder*='ocation']"},
			value:     c.Location,
		},
		{
			selectors: []string{"input[name*='linkedin']", "input[placeholder*='LinkedIn']"},
			value:     c.LinkedIn,
		},
		{
			selectors: []string{"input[name*='github']", "input[placeholder*='GitHub']", "input[placeholder*='Github']"},
			value:     c.GitHub,
		},
		{
			selectors: []string{"input#resume", "input[name='job_application[resume]']", "input[type='file'][accept*='pdf']"},
			value:     c.ResumePath,
			isFile:    true,
		},
	}

	for _, f := range fields {
		if err := s.fillField(page, f.selectors, f.value, f.isFile); err != nil {
			// non-fatal: not every form has every field
			continue
		}
	}

	if err := s.fillCoverLetter(page, pkg.CoverLetter); err != nil {
		fmt.Printf("  cover letter skipped: %v\n", err)
	}

	return nil
}

func (s *Submitter) fillField(page *rod.Page, selectors []string, value string, isFile bool) error {
	if value == "" {
		return nil
	}

	for _, sel := range selectors {
		el, err := page.Element(sel)
		if err != nil {
			continue
		}

		if isFile {
			return el.SetFiles([]string{value})
		}

		if err := el.SelectAllText(); err != nil {
			return err
		}
		return el.Input(value)
	}

	return fmt.Errorf("no element found")
}

func (s *Submitter) fillCoverLetter(page *rod.Page, text string) error {
	if text == "" {
		return nil
	}

	textAreaSelectors := []string{
		"#cover_letter_text",
		"textarea[name*='cover_letter']",
		"textarea[placeholder*='over letter']",
		"textarea[placeholder*='over Letter']",
	}

	for _, sel := range textAreaSelectors {
		el, err := page.Element(sel)
		if err != nil {
			continue
		}
		if err := el.SelectAllText(); err != nil {
			return err
		}
		return el.Input(text)
	}

	fileSelectors := []string{
		"input[name*='cover_letter'][type='file']",
		"input[accept*='pdf'][name*='cover']",
	}

	for _, sel := range fileSelectors {
		el, err := page.Element(sel)
		if err != nil {
			continue
		}
		tmp, err := writeTempCoverLetter(text)
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		return el.SetFiles([]string{tmp})
	}

	return fmt.Errorf("no cover letter field found")
}

func (s *Submitter) submit(page *rod.Page) error {
	submitSelectors := []string{
		"input[type='submit']",
		"button[type='submit']",
		"#submit_app",
		"button.submit-app",
	}

	for _, sel := range submitSelectors {
		el, err := page.Element(sel)
		if err != nil {
			continue
		}
		if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return err
		}
		_ = page.Keyboard.Press(input.Enter)
		return page.WaitIdle(30 * time.Second)
	}

	return fmt.Errorf("submit button not found")
}

func writeTempCoverLetter(text string) (string, error) {
	f, err := os.CreateTemp("", "scout-cover-*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = strings.NewReader(text).WriteTo(f)
	return f.Name(), err
}
