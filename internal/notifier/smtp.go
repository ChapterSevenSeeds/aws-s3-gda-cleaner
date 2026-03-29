package notifier

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"time"

	"github.com/balli/aws-s3-gda-cleaner/internal/deleter"
	"github.com/balli/aws-s3-gda-cleaner/internal/scanner"
)

// SMTPNotifier sends notifications via email.
type SMTPNotifier struct {
	host     string
	port     int
	username string
	password string
	from     string
	to       string
	useTLS   bool
}

// NewSMTPNotifier creates a new SMTP notifier.
func NewSMTPNotifier(host string, port int, username, password, from, to string, useTLS bool) *SMTPNotifier {
	return &SMTPNotifier{
		host:     host,
		port:     port,
		username: username,
		password: password,
		from:     from,
		to:       to,
		useTLS:   useTLS,
	}
}

func (n *SMTPNotifier) SendDeletionSummary(candidates []scanner.S3Object, approvalURL string) error {
	data := buildTemplateData(candidates)
	data["ApprovalURL"] = approvalURL
	data["HasApprovalURL"] = approvalURL != ""

	subject := fmt.Sprintf("S3 GDA Cleaner: %d stale files found (%s)", len(candidates), data["TotalSizeStr"])

	body, err := renderTemplate(summaryTemplate, data)
	if err != nil {
		return fmt.Errorf("rendering summary template: %w", err)
	}

	return n.sendEmail(subject, body)
}

func (n *SMTPNotifier) SendDeletionReport(candidates []scanner.S3Object, result *deleter.DeletionResult) error {
	data := buildTemplateData(candidates)
	data["Deleted"] = result.Deleted
	data["Failed"] = result.Failed
	data["FreedSizeStr"] = formatSize(result.TotalSize)
	data["Errors"] = result.Errors
	data["HasErrors"] = len(result.Errors) > 0

	subject := fmt.Sprintf("S3 GDA Cleaner: %d files deleted (%s freed)", result.Deleted, data["FreedSizeStr"])

	body, err := renderTemplate(reportTemplate, data)
	if err != nil {
		return fmt.Errorf("rendering report template: %w", err)
	}

	return n.sendEmail(subject, body)
}

func (n *SMTPNotifier) sendEmail(subject, htmlBody string) error {
	addr := net.JoinHostPort(n.host, fmt.Sprintf("%d", n.port))

	headers := map[string]string{
		"From":         n.from,
		"To":           n.to,
		"Subject":      subject,
		"MIME-Version": "1.0",
		"Content-Type": "text/html; charset=\"UTF-8\"",
	}

	var msg bytes.Buffer
	for k, v := range headers {
		fmt.Fprintf(&msg, "%s: %s\r\n", k, v)
	}
	msg.WriteString("\r\n")
	msg.WriteString(htmlBody)

	var auth smtp.Auth
	if n.username != "" {
		auth = smtp.PlainAuth("", n.username, n.password, n.host)
	}

	if n.useTLS {
		return n.sendTLS(addr, auth, msg.Bytes())
	}
	return smtp.SendMail(addr, auth, n.from, []string{n.to}, msg.Bytes())
}

func (n *SMTPNotifier) sendTLS(addr string, auth smtp.Auth, msg []byte) error {
	tlsConfig := &tls.Config{
		ServerName: n.host,
	}

	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("TLS dial: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, n.host)
	if err != nil {
		return fmt.Errorf("creating SMTP client: %w", err)
	}
	defer client.Close()

	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
	}

	if err := client.Mail(n.from); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	if err := client.Rcpt(n.to); err != nil {
		return fmt.Errorf("SMTP RCPT TO: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("writing email body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("closing email writer: %w", err)
	}

	slog.Info("email sent", "to", n.to)
	return client.Quit()
}

type fileEntry struct {
	Name    string
	SizeStr string
	AgeStr  string
}

func buildTemplateData(candidates []scanner.S3Object) map[string]any {
	now := time.Now()

	// Sort by key for consistent display
	sorted := make([]scanner.S3Object, len(candidates))
	copy(sorted, candidates)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Key < sorted[j].Key
	})

	var totalSize int64
	files := make([]fileEntry, len(sorted))
	for i, obj := range sorted {
		totalSize += obj.Size
		files[i] = fileEntry{
			Name:    obj.Key,
			SizeStr: formatSize(obj.Size),
			AgeStr:  formatAge(now.Sub(obj.LastModified)),
		}
	}

	return map[string]any{
		"Files":        files,
		"FileCount":    len(files),
		"TotalSize":    totalSize,
		"TotalSizeStr": formatSize(totalSize),
		"Timestamp":    now.UTC().Format(time.RFC3339),
	}
}

func formatSize(bytes int64) string {
	if bytes == 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := int(math.Log(float64(bytes)) / math.Log(1024))
	if i >= len(units) {
		i = len(units) - 1
	}
	val := float64(bytes) / math.Pow(1024, float64(i))
	if i == 0 {
		return fmt.Sprintf("%d B", bytes)
	}
	return fmt.Sprintf("%.2f %s", val, units[i])
}

func formatAge(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days > 365 {
		years := days / 365
		remaining := days % 365
		return fmt.Sprintf("%dy %dd", years, remaining)
	}
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

func renderTemplate(tmplStr string, data map[string]any) (string, error) {
	tmpl, err := template.New("email").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var summaryTemplate = strings.TrimSpace(`
<!DOCTYPE html>
<html>
<head>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 20px; background: #f5f5f5; }
  .container { max-width: 800px; margin: 0 auto; background: #fff; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); overflow: hidden; }
  .header { background: #d97706; color: white; padding: 20px 30px; }
  .header h1 { margin: 0; font-size: 22px; }
  .content { padding: 20px 30px; }
  .summary { background: #fef3c7; border: 1px solid #f59e0b; border-radius: 6px; padding: 15px; margin-bottom: 20px; }
  table { width: 100%; border-collapse: collapse; margin: 15px 0; }
  th { background: #f3f4f6; text-align: left; padding: 10px 12px; border-bottom: 2px solid #e5e7eb; font-size: 13px; text-transform: uppercase; color: #6b7280; }
  td { padding: 8px 12px; border-bottom: 1px solid #f3f4f6; font-size: 14px; }
  tr:hover { background: #f9fafb; }
  .size { text-align: right; white-space: nowrap; }
  .age { text-align: right; white-space: nowrap; }
  .approve-btn { display: inline-block; background: #dc2626; color: white; padding: 12px 30px; text-decoration: none; border-radius: 6px; font-weight: bold; font-size: 16px; margin: 15px 0; }
  .approve-btn:hover { background: #b91c1c; }
  .warning { color: #92400e; font-weight: 500; }
  .footer { padding: 15px 30px; background: #f9fafb; color: #6b7280; font-size: 12px; border-top: 1px solid #e5e7eb; }
</style>
</head>
<body>
<div class="container">
  <div class="header">
    <h1>⚠ Stale Files Detected in S3</h1>
  </div>
  <div class="content">
    <div class="summary">
      <strong>{{.FileCount}} stale file(s)</strong> found totaling <strong>{{.TotalSizeStr}}</strong>.
      <br>These files exist in S3 but no longer exist on the local filesystem.
    </div>

    <table>
      <thead>
        <tr><th>File</th><th class="size">Size</th><th class="age">Age</th></tr>
      </thead>
      <tbody>
        {{range .Files}}
        <tr><td>{{.Name}}</td><td class="size">{{.SizeStr}}</td><td class="age">{{.AgeStr}}</td></tr>
        {{end}}
      </tbody>
    </table>

    {{if .HasApprovalURL}}
    <p class="warning">Click the button below to permanently delete these files from S3 Glacier Deep Archive. This action cannot be undone.</p>
    <a href="{{.ApprovalURL}}" class="approve-btn">Approve Deletion</a>
    {{else}}
    <p>These files will be deleted automatically.</p>
    {{end}}
  </div>
  <div class="footer">
    Generated at {{.Timestamp}} by S3 GDA Cleaner
  </div>
</div>
</body>
</html>
`)

var reportTemplate = strings.TrimSpace(`
<!DOCTYPE html>
<html>
<head>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 20px; background: #f5f5f5; }
  .container { max-width: 800px; margin: 0 auto; background: #fff; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); overflow: hidden; }
  .header { background: #059669; color: white; padding: 20px 30px; }
  .header h1 { margin: 0; font-size: 22px; }
  .header.error { background: #dc2626; }
  .content { padding: 20px 30px; }
  .summary { background: #d1fae5; border: 1px solid #10b981; border-radius: 6px; padding: 15px; margin-bottom: 20px; }
  .summary.error { background: #fee2e2; border-color: #ef4444; }
  table { width: 100%; border-collapse: collapse; margin: 15px 0; }
  th { background: #f3f4f6; text-align: left; padding: 10px 12px; border-bottom: 2px solid #e5e7eb; font-size: 13px; text-transform: uppercase; color: #6b7280; }
  td { padding: 8px 12px; border-bottom: 1px solid #f3f4f6; font-size: 14px; }
  tr:hover { background: #f9fafb; }
  .size { text-align: right; white-space: nowrap; }
  .age { text-align: right; white-space: nowrap; }
  .error-list { background: #fee2e2; border: 1px solid #ef4444; border-radius: 6px; padding: 15px; margin: 15px 0; }
  .error-list li { color: #991b1b; margin: 4px 0; font-size: 13px; }
  .footer { padding: 15px 30px; background: #f9fafb; color: #6b7280; font-size: 12px; border-top: 1px solid #e5e7eb; }
</style>
</head>
<body>
<div class="container">
  <div class="header{{if .HasErrors}} error{{end}}">
    <h1>{{if .HasErrors}}⚠ Deletion Completed with Errors{{else}}✓ Deletion Complete{{end}}</h1>
  </div>
  <div class="content">
    <div class="summary{{if .HasErrors}} error{{end}}">
      <strong>{{.Deleted}}</strong> file(s) deleted, freeing <strong>{{.FreedSizeStr}}</strong>.
      {{if .HasErrors}}<br><strong>{{.Failed}}</strong> file(s) failed to delete.{{end}}
    </div>

    <table>
      <thead>
        <tr><th>File</th><th class="size">Size</th><th class="age">Age</th></tr>
      </thead>
      <tbody>
        {{range .Files}}
        <tr><td>{{.Name}}</td><td class="size">{{.SizeStr}}</td><td class="age">{{.AgeStr}}</td></tr>
        {{end}}
      </tbody>
    </table>

    {{if .HasErrors}}
    <div class="error-list">
      <strong>Errors:</strong>
      <ul>
        {{range .Errors}}
        <li>{{.}}</li>
        {{end}}
      </ul>
    </div>
    {{end}}
  </div>
  <div class="footer">
    Generated at {{.Timestamp}} by S3 GDA Cleaner
  </div>
</div>
</body>
</html>
`)
