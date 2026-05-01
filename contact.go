package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"time"
)

var contactLimiter = newRateLimiter(3, time.Hour)

type contactPageData struct {
	Sent  bool
	Error string
}

func handleContactPage(w http.ResponseWriter, r *http.Request) {
	renderHTML(w, "contact.html", contactPageData{
		Sent:  r.URL.Query().Get("sent") == "1",
		Error: r.URL.Query().Get("error"),
	})
}

func handleContactSubmit(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)

	if err := r.ParseForm(); err != nil {
		redirectContact(w, r, "error=invalid")
		return
	}

	// Honeypot: real users leave this blank; bots fill it in. Pretend success
	// (and skip the rate limiter) so bots don't learn the trick.
	if r.PostForm.Get("website") != "" {
		slog.Info("contact honeypot", "ip", ip)
		redirectContact(w, r, "sent=1")
		return
	}

	if !contactLimiter.allow(ip) {
		slog.Warn("contact rate limit exceeded", "ip", ip)
		redirectContact(w, r, "error=rate")
		return
	}

	message := strings.TrimSpace(r.PostForm.Get("message"))
	replyEmail := strings.TrimSpace(r.PostForm.Get("reply_email"))

	if len(message) < 5 || len(message) > 5000 {
		redirectContact(w, r, "error=invalid")
		return
	}
	if replyEmail != "" {
		if _, err := mail.ParseAddress(replyEmail); err != nil {
			redirectContact(w, r, "error=invalid")
			return
		}
	}

	go func() {
		if err := sendContactEmail(replyEmail, message, ip); err != nil {
			slog.Error("contact send", "error", err, "ip", ip)
		}
	}()

	redirectContact(w, r, "sent=1")
}

func redirectContact(w http.ResponseWriter, r *http.Request, query string) {
	http.Redirect(w, r, "/contact?"+query, http.StatusSeeOther)
}

func sendContactEmail(replyEmail, message, ip string) error {
	smtpUser := os.Getenv("SMTP_USER")
	smtpPass := os.Getenv("SMTP_PASS")
	to := os.Getenv("CONTACT_TO_EMAIL")
	if to == "" {
		to = smtpUser
	}

	// Local-dev shortcut: if SMTP isn't configured, log the message so the
	// form is testable end-to-end without credentials.
	if smtpUser == "" || smtpPass == "" {
		slog.Info("contact form skipped",
			"reason", "smtp not configured",
			"reply_email", replyEmail, "ip", ip, "message", message)
		return nil
	}

	subject := "Train contact"
	if preview := firstLine(message); preview != "" {
		subject = "Train contact: " + truncate(preview, 60)
	}

	replyTo := smtpUser
	displayReply := "(not provided)"
	if replyEmail != "" {
		replyTo = replyEmail
		displayReply = replyEmail
	}

	body := fmt.Sprintf(
		"Reply email: %s\nIP: %s\nSubmitted: %s\n\n%s\n",
		displayReply, ip, time.Now().UTC().Format(time.RFC3339), message,
	)

	msg := []byte(strings.Join([]string{
		"From: Train <" + smtpUser + ">",
		"To: " + to,
		"Reply-To: " + replyTo,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n"))

	auth := smtp.PlainAuth("", smtpUser, smtpPass, "smtp.gmail.com")
	return smtp.SendMail("smtp.gmail.com:587", auth, smtpUser, []string{to}, msg)
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
