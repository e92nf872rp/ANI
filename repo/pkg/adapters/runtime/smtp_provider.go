package runtime

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

// smtpProvider implements ports.SMTPProvider using Go's net/smtp package.
type smtpProvider struct{}

func NewSMTPProvider() ports.SMTPProvider {
	return &smtpProvider{}
}

func (p *smtpProvider) Send(ctx context.Context, req ports.SMTPSendRequest) (ports.SMTPSendResult, error) {
	slog.Info("SMTP send: starting",
		"host", req.SmtpHost,
		"port", req.SmtpPort,
		"encryption", req.Encryption,
		"from", req.FromAddress,
		"to", strings.Join(req.ToAddresses, ", "),
		"username", req.Username,
		"password_len", len(req.Password),
	)

	if req.SmtpHost == "" {
		return ports.SMTPSendResult{Sent: false, Err: "smtp_host is empty"}, nil
	}
	if req.SmtpPort <= 0 || req.SmtpPort > 65535 {
		return ports.SMTPSendResult{Sent: false, Err: "smtp_port out of range"}, nil
	}
	if req.FromAddress == "" {
		return ports.SMTPSendResult{Sent: false, Err: "from_address is empty"}, nil
	}
	if len(req.ToAddresses) == 0 {
		return ports.SMTPSendResult{Sent: false, Err: "no recipients"}, nil
	}

	addr := fmt.Sprintf("%s:%d", req.SmtpHost, req.SmtpPort)
	msg := buildRFC822Message(req.FromAddress, req.ToAddresses, req.Subject, req.Body)

	var smtpErr error
	switch req.Encryption {
	case "ssl":
		slog.Info("SMTP send: using SSL/TLS connection", "addr", addr)
		smtpErr = sendSSL(addr, req.Username, req.Password, req.FromAddress, req.ToAddresses, msg)
	case "starttls":
		slog.Info("SMTP send: using STARTTLS connection", "addr", addr)
		smtpErr = sendSTARTTLS(addr, req.Username, req.Password, req.FromAddress, req.ToAddresses, msg)
	case "none", "":
		slog.Info("SMTP send: using plain connection", "addr", addr)
		smtpErr = sendPlain(addr, req.Username, req.Password, req.FromAddress, req.ToAddresses, msg)
	default:
		return ports.SMTPSendResult{Sent: false, Err: fmt.Sprintf("unsupported encryption: %s", req.Encryption)}, nil
	}

	if smtpErr != nil {
		slog.Error("SMTP send: failed",
			"addr", addr,
			"encryption", req.Encryption,
			"error", smtpErr.Error(),
		)
		return ports.SMTPSendResult{Sent: false, Err: smtpErr.Error()}, nil
	}

	slog.Info("SMTP send: success",
		"addr", addr,
		"from", req.FromAddress,
		"to", strings.Join(req.ToAddresses, ", "),
	)
	return ports.SMTPSendResult{Sent: true}, nil
}

func buildRFC822Message(from string, to []string, subject, body string) []byte {
	headers := make(map[string]string)
	headers["From"] = from
	headers["To"] = strings.Join(to, ", ")
	headers["Subject"] = subject
	headers["MIME-Version"] = "1.0"
	headers["Content-Type"] = "text/plain; charset=UTF-8"
	headers["Content-Transfer-Encoding"] = "8bit"

	var buf strings.Builder
	for k, v := range headers {
		fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
	}
	buf.WriteString("\r\n")
	buf.WriteString(body)

	return []byte(buf.String())
}

func sendPlain(addr, username, password, from string, to []string, msg []byte) error {
	var auth smtp.Auth
	if username != "" && password != "" {
		slog.Info("SMTP auth: plain, using credentials",
			"username", username,
			"host", strings.Split(addr, ":")[0],
		)
		auth = smtp.PlainAuth("", username, password, strings.Split(addr, ":")[0])
	} else {
		slog.Info("SMTP auth: plain, no credentials (anonymous)")
	}
	return smtp.SendMail(addr, auth, from, to, msg)
}

func sendSSL(addr, username, password, from string, to []string, msg []byte) error {
	host := strings.Split(addr, ":")[0]
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}

	slog.Info("SMTP SSL: dialing", "addr", addr)
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("SSL dial %s: %w", addr, err)
	}
	defer conn.Close()

	slog.Info("SMTP SSL: connected, creating client", "host", host)
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("SMTP client over SSL: %w", err)
	}
	defer client.Close()

	return sendWithClient(client, host, username, password, from, to, msg)
}

func sendSTARTTLS(addr, username, password, from string, to []string, msg []byte) error {
	host := strings.Split(addr, ":")[0]
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}

	slog.Info("SMTP STARTTLS: dialing", "addr", addr)
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("SMTP dial %s: %w", addr, err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		slog.Info("SMTP STARTTLS: server supports STARTTLS, upgrading", "host", host)
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	} else {
		slog.Info("SMTP STARTTLS: server does not support STARTTLS, using plain", "host", host)
	}

	return sendWithClient(client, host, username, password, from, to, msg)
}

func sendWithClient(client *smtp.Client, host, username, password, from string, to []string, msg []byte) error {
	// AUTH
	if username != "" && password != "" {
		if host == "" {
			host = "localhost"
		}
		slog.Info("SMTP AUTH: starting",
			"username", username,
			"host", host,
			"password_len", len(password),
		)
		auth := smtp.PlainAuth("", username, password, host)
		if err := client.Auth(auth); err != nil {
			slog.Error("SMTP AUTH: failed",
				"username", username,
				"host", host,
				"error", err.Error(),
			)
			return fmt.Errorf("SMTP auth: %w", err)
		}
		slog.Info("SMTP AUTH: success", "username", username)
	} else {
		slog.Info("SMTP AUTH: skipped (no username or password)",
			"username_len", len(username),
			"password_len", len(password),
		)
	}

	// MAIL FROM
	slog.Info("SMTP MAIL FROM", "from", from)
	if err := client.Mail(from); err != nil {
		slog.Error("SMTP MAIL FROM: failed", "from", from, "error", err.Error())
		return fmt.Errorf("MAIL FROM: %w", err)
	}

	// RCPT TO
	for _, addr := range to {
		slog.Info("SMTP RCPT TO", "to", addr)
		if err := client.Rcpt(addr); err != nil {
			slog.Error("SMTP RCPT TO: failed", "to", addr, "error", err.Error())
			return fmt.Errorf("RCPT TO %s: %w", addr, err)
		}
	}

	// DATA
	slog.Info("SMTP DATA: writing message body", "body_len", len(msg))
	w, err := client.Data()
	if err != nil {
		slog.Error("SMTP DATA: failed to open", "error", err.Error())
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		slog.Error("SMTP DATA: write failed", "error", err.Error())
		return fmt.Errorf("write body: %w", err)
	}
	if err := w.Close(); err != nil {
		slog.Error("SMTP DATA: close failed", "error", err.Error())
		return fmt.Errorf("close body: %w", err)
	}
	slog.Info("SMTP DATA: body sent successfully")

	// QUIT
	slog.Info("SMTP QUIT: closing connection")
	if err := client.Quit(); err != nil {
		slog.Error("SMTP QUIT: failed", "error", err.Error())
		return fmt.Errorf("QUIT: %w", err)
	}

	return nil
}
