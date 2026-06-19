package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/mbcaira/scout/internal/generator"
	"github.com/mbcaira/scout/internal/notifier"
	"github.com/mbcaira/scout/internal/scraper"
	"github.com/mbcaira/scout/internal/scorer"
	"github.com/mbcaira/scout/internal/store"
	"github.com/mbcaira/scout/internal/submitter"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Email struct {
		SMTPHost string `yaml:"smtp_host"`
		SMTPPort int    `yaml:"smtp_port"`
	} `yaml:"email"`
	Targets []struct {
		Name       string   `yaml:"name"`
		CareersURL string   `yaml:"careers_url"`
		Keywords   []string `yaml:"keywords"`
		ATS        string   `yaml:"ats"`
		AutoApply  bool     `yaml:"auto_apply"`
	} `yaml:"targets"`
	Filters struct {
		ExcludeKeywords []string `yaml:"exclude_keywords"`
		Locations       []string `yaml:"locations"`
	} `yaml:"filters"`
}

type Candidate struct {
	Name       string
	Email      string
	Phone      string
	Location   string
	ResumePath string
	LinkedIn   string
	GitHub     string
	Profile    string
}

func loadConfig(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	return cfg, yaml.Unmarshal(data, &cfg)
}

func loadCandidate() (Candidate, error) {
	c := Candidate{
		Name:       os.Getenv("CANDIDATE_NAME"),
		Email:      os.Getenv("CANDIDATE_EMAIL"),
		Phone:      os.Getenv("CANDIDATE_PHONE"),
		Location:   os.Getenv("CANDIDATE_LOCATION"),
		ResumePath: os.Getenv("CANDIDATE_RESUME_PATH"),
		LinkedIn:   os.Getenv("CANDIDATE_LINKEDIN"),
		GitHub:     os.Getenv("CANDIDATE_GITHUB"),
		Profile:    os.Getenv("CANDIDATE_PROFILE"),
	}
	if c.Name == "" || c.Email == "" {
		return c, fmt.Errorf("CANDIDATE_NAME and CANDIDATE_EMAIL are required")
	}
	return c, nil
}

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("warning: could not load .env: %v", err)
	}

	var cfgPath string

	root := &cobra.Command{
		Use:   "scout",
		Short: "Automated job scout and application pipeline",
	}

	root.PersistentFlags().StringVarP(&cfgPath, "config", "c", "config/config.yaml", "config file path")

	root.AddCommand(
		runCmd(&cfgPath),
		listCmd(),
		applyCmd(),
	)

	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

func runCmd(cfgPath *string) *cobra.Command {
	var scoreThreshold int
	var dryRun bool
	var rescore bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Scrape, score, and auto-apply to matching jobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			candidate, err := loadCandidate()
			if err != nil {
				return err
			}

			apiKey := os.Getenv("ANTHROPIC_API_KEY")
			if apiKey == "" {
				return fmt.Errorf("ANTHROPIC_API_KEY is required")
			}

			db, err := store.New("scout.db")
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer db.Close()

			var targets []scraper.Target
			for _, t := range cfg.Targets {
				targets = append(targets, scraper.Target{
					Name:       t.Name,
					CareersURL: t.CareersURL,
					Keywords:   t.Keywords,
					ATS:        t.ATS,
					AutoApply:  t.AutoApply,
				})
			}

			sc := scraper.New(scraper.Config{
				Targets:         targets,
				ExcludeKeywords: cfg.Filters.ExcludeKeywords,
				Locations:       cfg.Filters.Locations,
			})

			fmt.Println("scraping...")
			jobs, err := sc.Run()
			if err != nil {
				return fmt.Errorf("scrape: %w", err)
			}
			fmt.Printf("found %d candidate jobs\n", len(jobs))

			scr := scorer.New(apiKey, candidate.Profile)
			gen := generator.New(apiKey, candidate.Profile)

			nameParts := strings.SplitN(candidate.Name, " ", 2)
			firstName, lastName := nameParts[0], ""
			if len(nameParts) > 1 {
				lastName = nameParts[1]
			}

			sub := submitter.New(submitter.Candidate{
				FirstName:  firstName,
				LastName:   lastName,
				Email:      candidate.Email,
				Phone:      candidate.Phone,
				Location:   candidate.Location,
				ResumePath: candidate.ResumePath,
				LinkedIn:   candidate.LinkedIn,
				GitHub:     candidate.GitHub,
			})

			emailPass := os.Getenv("EMAIL_APP_PASSWORD")
			emailFrom := os.Getenv("EMAIL_FROM")
			emailTo := os.Getenv("EMAIL_TO")
			var ntf *notifier.Notifier
			if emailPass != "" && emailFrom != "" && emailTo != "" {
				ntf = notifier.New(notifier.Config{
					SMTPHost: cfg.Email.SMTPHost,
					SMTPPort: cfg.Email.SMTPPort,
					From:     emailFrom,
					Password: emailPass,
					To:       emailTo,
				})
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			for _, job := range jobs {
				if !rescore {
					seen, err := db.Seen(job.ID)
					if err != nil {
						return err
					}
					if seen {
						continue
					}
				}

				result, err := scr.Score(ctx, job)
				if err != nil {
					fmt.Printf("score error for %s: %v\n", job.Title, err)
					continue
				}

				job.Score = result.Score
				fmt.Printf("[%d] %s at %s — %s\n", result.Score, job.Title, job.Company, result.Reasoning)

				if rescore {
					if err := db.Upsert(job); err != nil {
						return err
					}
				} else if err := db.Save(job); err != nil {
					return err
				}

				if !result.Relevant || result.Score < scoreThreshold {
					if err := db.UpdateStatus(job.ID, "skipped"); err != nil {
						return err
					}
					continue
				}

				pkg, err := gen.Generate(ctx, job, nil)
				if err != nil {
					fmt.Printf("generate error for %s: %v\n", job.Title, err)
					continue
				}

				if dryRun {
					fmt.Printf("\n--- DRY RUN: %s at %s ---\n%s\n\n", job.Title, job.Company, pkg.CoverLetter)
					continue
				}

				if !job.AutoApply {
					fmt.Printf("manual ATS — emailing application: %s at %s\n", job.Title, job.Company)
					if ntf != nil {
						if err := ntf.Send(job, pkg); err != nil {
							fmt.Printf("notify error for %s: %v\n", job.Title, err)
						}
					} else {
						fmt.Printf("  (no email config — skipping notification)\n")
					}
					continue
				}

				if err := sub.Submit(job, pkg); err != nil {
					fmt.Printf("submit failed for %s: %v\n", job.Title, err)
					if dbErr := db.UpdateStatus(job.ID, "failed"); dbErr != nil {
						fmt.Printf("db error: %v\n", dbErr)
					}
					continue
				}

				fmt.Printf("applied: %s at %s\n", job.Title, job.Company)
				if err := db.UpdateStatus(job.ID, "applied"); err != nil {
					return err
				}
			}

			return nil
		},
	}

	cmd.Flags().IntVarP(&scoreThreshold, "threshold", "t", 70, "minimum score to auto-apply (0-100)")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "d", false, "score and generate without submitting")
	cmd.Flags().BoolVarP(&rescore, "rescore", "r", false, "re-score already seen jobs")

	return cmd
}

func listCmd() *cobra.Command {
	var status string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List jobs in the database",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := store.New("scout.db")
			if err != nil {
				return err
			}
			defer db.Close()

			jobs, err := db.ByStatus(status)
			if err != nil {
				return err
			}

			if len(jobs) == 0 {
				fmt.Printf("no %s jobs\n", status)
				return nil
			}

			for _, j := range jobs {
				fmt.Printf("[%s] [%d] %s at %s\n  %s\n  %s\n\n", j.Status, j.Score, j.Title, j.Company, j.Location, j.URL)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&status, "status", "s", "pending", "filter by status: pending, applied, failed, skipped, all")
	return cmd
}

func applyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply <job-id>",
		Short: "Manually apply to a specific job by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]

			candidate, err := loadCandidate()
			if err != nil {
				return err
			}

			apiKey := os.Getenv("ANTHROPIC_API_KEY")
			if apiKey == "" {
				return fmt.Errorf("ANTHROPIC_API_KEY is required")
			}

			db, err := store.New("scout.db")
			if err != nil {
				return err
			}
			defer db.Close()

			job, err := db.Get(jobID)
			if err != nil {
				return fmt.Errorf("job not found: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			gen := generator.New(apiKey, candidate.Profile)
			pkg, err := gen.Generate(ctx, job, nil)
			if err != nil {
				return fmt.Errorf("generate content: %w", err)
			}

			fmt.Printf("\n--- COVER LETTER ---\n%s\n\n", pkg.CoverLetter)

			nameParts := strings.SplitN(candidate.Name, " ", 2)
			firstName, lastName := nameParts[0], ""
			if len(nameParts) > 1 {
				lastName = nameParts[1]
			}

			sub := submitter.New(submitter.Candidate{
				FirstName:  firstName,
				LastName:   lastName,
				Email:      candidate.Email,
				Phone:      candidate.Phone,
				Location:   candidate.Location,
				ResumePath: candidate.ResumePath,
				LinkedIn:   candidate.LinkedIn,
				GitHub:     candidate.GitHub,
			})

			if err := sub.Submit(job, pkg); err != nil {
				return fmt.Errorf("submit: %w", err)
			}

			return db.UpdateStatus(jobID, "applied")
		},
	}
}
