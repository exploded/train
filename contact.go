package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// 3 submissions per hour per IP. Sits on top of the global 120/min limiter.
var contactLimiter = newRateLimiter(3, time.Hour)

type contactPageData struct {
	Sent  bool
	Error string
}

func handleContactPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := contactPageData{
		Sent:  r.URL.Query().Get("sent") == "1",
		Error: r.URL.Query().Get("error"),
	}
	if err := templates.ExecuteTemplate(w, "contact.html", data); err != nil {
		slog.Error("contact template", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func handleContactSubmit(w http.ResponseWriter, r *http.Request) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/contact?error=invalid", http.StatusSeeOther)
		return
	}

	// Honeypot: real users leave this blank; bots fill it in.
	if r.PostForm.Get("website") != "" {
		slog.Info("contact honeypot", "ip", ip)
		// Pretend success so bots don't learn the trick.
		http.Redirect(w, r, "/contact?sent=1", http.StatusSeeOther)
		return
	}

	if !contactLimiter.allow(ip) {
		slog.Warn("contact rate limit exceeded", "ip", ip)
		http.Redirect(w, r, "/contact?error=rate", http.StatusSeeOther)
		return
	}

	message := strings.TrimSpace(r.PostForm.Get("message"))
	replyEmail := strings.TrimSpace(r.PostForm.Get("reply_email"))

	if len(message) < 5 || len(message) > 5000 {
		http.Redirect(w, r, "/contact?error=invalid", http.StatusSeeOther)
		return
	}
	if replyEmail != "" && !looksLikeEmail(replyEmail) {
		http.Redirect(w, r, "/contact?error=invalid", http.StatusSeeOther)
		return
	}

	if err := sendContactEmail(replyEmail, message, ip); err != nil {
		slog.Error("contact send", "error", err, "ip", ip)
		http.Redirect(w, r, "/contact?error=smtp", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/contact?sent=1", http.StatusSeeOther)
}

func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at < 1 || at == len(s)-1 {
		return false
	}
	if !strings.Contains(s[at+1:], ".") {
		return false
	}
	if strings.ContainsAny(s, " \t\r\n<>") {
		return false
	}
	return true
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
	if replyEmail != "" {
		replyTo = replyEmail
	}

	body := fmt.Sprintf(
		"Reply email: %s\nIP: %s\nSubmitted: %s\n\n%s\n",
		valueOrDash(replyEmail), ip, time.Now().UTC().Format(time.RFC3339), message,
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

func valueOrDash(s string) string {
	if s == "" {
		return "(not provided)"
	}
	return s
}
