# scout

Automated job application pipeline. Scrapes careers pages, scores postings against your profile using Claude, generates tailored cover letters, and auto-submits Greenhouse applications — all unattended.

For companies on custom ATS (Jane Street, HRT), it emails you a ready-to-paste application package instead.

## How it works

1. **Scrape** — visits each target's careers page and collects job listings
2. **Score** — sends each job + your profile to Claude Haiku; gets a 0–100 relevance score
3. **Generate** — for jobs above your threshold, Claude writes a tailored cover letter
4. **Submit** — Greenhouse jobs are auto-submitted via headless Chrome; custom ATS jobs are emailed to you with the cover letter pre-filled

## Setup

### Prerequisites

- Go 1.23+
- Chrome (auto-downloaded on first run via rod)
- An [Anthropic API key](https://console.anthropic.com/)
- A Gmail App Password (for email notifications)

### Install

```bash
git clone https://github.com/mbcaira/scout
cd scout
go build -o scout ./cmd/scout
```

### Configure

Copy `.env.example` to `.env` and fill in your details:

```bash
cp .env.example .env
```

Edit `config/config.yaml` to set your target companies, keywords, and filters. The `auto_apply` flag controls whether scout submits automatically (Greenhouse) or emails you the application package (custom ATS).

## Usage

```bash
# Scrape, score, and apply
./scout run

# Dry run — score and generate without submitting
./scout run --dry-run

# Re-score jobs already in the database
./scout run --rescore

# Set a custom score threshold (default: 70)
./scout run --threshold 80

# List pending jobs in the database
./scout list

# Manually apply to a specific job by ID
./scout apply <job-id>
```

## Config

```yaml
targets:
  - name: "Cloudflare"
    careers_url: "https://job-boards.greenhouse.io/cloudflare"
    keywords: ["rust", "networking", "systems"]
    ats: "greenhouse"
    auto_apply: true   # headless form submission

  - name: "Jane Street"
    careers_url: "https://www.janestreet.com/join-jane-street/open-roles/"
    keywords: ["low-latency", "systems", "infrastructure"]
    ats: "custom"
    auto_apply: false  # emails you the application package

filters:
  exclude_keywords: ["javascript", "typescript", "frontend"]
  locations: ["Toronto", "New York", "Remote"]
```

## Scheduling

Run on a cron to check for new postings throughout the day:

```bash
# Every 2 hours, 8am–6pm
0 8-18/2 * * * cd /path/to/scout && ./scout run >> scout.log 2>&1
```

## Stack

- Go — CLI, scraping, orchestration
- [rod](https://github.com/go-rod/rod) — headless Chrome for scraping and form submission
- [Claude Haiku](https://anthropic.com) — scoring and cover letter generation
- SQLite — deduplication and job tracking
