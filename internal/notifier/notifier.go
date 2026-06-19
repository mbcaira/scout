package notifier

import (
	"fmt"
	"net/smtp"
	"strings"

	"github.com/jordan-wright/email"
	"github.com/mbcaira/scout/internal/generator"
	"github.com/mbcaira/scout/internal/store"
)

type Config struct {
	SMTPHost string
	SMTPPort int
	From     string
	Password string
	To       string
}

type Notifier struct {
	cfg Config
}

func New(cfg Config) *Notifier {
	return &Notifier{cfg: cfg}
}

func (n *Notifier) Send(job store.Job, pkg generator.Package) error {
	e := email.NewEmail()
	e.From = n.cfg.From
	e.To = []string{n.cfg.To}
	e.Subject = fmt.Sprintf("[scout] New match: %s at %s", job.Title, job.Company)
	e.Text = []byte(n.format(job, pkg))

	addr := fmt.Sprintf("%s:%d", n.cfg.SMTPHost, n.cfg.SMTPPort)
	auth := smtp.PlainAuth("", n.cfg.From, n.cfg.Password, n.cfg.SMTPHost)

	return e.Send(addr, auth)
}

func (n *Notifier) format(job store.Job, pkg generator.Package) string {
	var b strings.Builder

	fmt.Fprintf(&b, "JOB: %s at %s\n", job.Title, job.Company)
	fmt.Fprintf(&b, "URL: %s\n", job.URL)
	fmt.Fprintf(&b, "Location: %s\n\n", job.Location)
	fmt.Fprintf(&b, "--- COVER LETTER ---\n\n%s\n\n", pkg.CoverLetter)

	if len(pkg.Questions) > 0 {
		fmt.Fprintf(&b, "--- APPLICATION QUESTIONS ---\n\n")
		for q, a := range pkg.Questions {
			fmt.Fprintf(&b, "Q: %s\nA: %s\n\n", q, a)
		}
	}

	fmt.Fprintf(&b, "---\nApply at: %s\n", job.URL)
	return b.String()
}
