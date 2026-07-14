package runtime

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestSMTPProvider_Send_RejectsEmptyHost(t *testing.T) {
	p := NewSMTPProvider()
	result, err := p.Send(context.Background(), ports.SMTPSendRequest{
		SmtpHost: "",
		SmtpPort: 465,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result.Sent {
		t.Fatal("Sent should be false for empty host")
	}
	if result.Err == "" {
		t.Fatal("Err should be non-empty")
	}
}

func TestSMTPProvider_Send_RejectsBadPort(t *testing.T) {
	p := NewSMTPProvider()
	result, _ := p.Send(context.Background(), ports.SMTPSendRequest{
		SmtpHost: "smtp.example.com", SmtpPort: 0,
	})
	if result.Sent {
		t.Fatal("Sent should be false for port 0")
	}

	result, _ = p.Send(context.Background(), ports.SMTPSendRequest{
		SmtpHost: "smtp.example.com", SmtpPort: 70000,
	})
	if result.Sent {
		t.Fatal("Sent should be false for port > 65535")
	}
}

func TestSMTPProvider_Send_RejectsEmptyFromAddress(t *testing.T) {
	p := NewSMTPProvider()
	result, _ := p.Send(context.Background(), ports.SMTPSendRequest{
		SmtpHost: "smtp.example.com", SmtpPort: 465,
	})
	if result.Sent {
		t.Fatal("Sent should be false for empty from address")
	}
}

func TestSMTPProvider_Send_RejectsNoRecipients(t *testing.T) {
	p := NewSMTPProvider()
	result, _ := p.Send(context.Background(), ports.SMTPSendRequest{
		SmtpHost: "smtp.example.com", SmtpPort: 465,
		FromAddress: "from@example.com",
	})
	if result.Sent {
		t.Fatal("Sent should be false for no recipients")
	}
}

func TestSMTPProvider_Send_RejectsUnsupportedEncryption(t *testing.T) {
	p := NewSMTPProvider()
	result, _ := p.Send(context.Background(), ports.SMTPSendRequest{
		SmtpHost: "smtp.example.com", SmtpPort: 465,
		FromAddress: "from@example.com",
		ToAddresses: []string{"to@example.com"},
		Encryption: "tls",
	})
	if result.Sent {
		t.Fatal("Sent should be false for unsupported encryption")
	}
}

func TestBuildRFC822Message_ContainsHeaders(t *testing.T) {
	msg := buildRFC822Message(
		"from@example.com",
		[]string{"to1@example.com", "to2@example.com"},
		"Test Subject",
		"Hello body",
	)
	s := string(msg)
	if !contains(s, "From: from@example.com\r\n") {
		t.Fatal("missing From header")
	}
	if !contains(s, "To: to1@example.com, to2@example.com\r\n") {
		t.Fatal("missing To header")
	}
	if !contains(s, "Subject: Test Subject\r\n") {
		t.Fatal("missing Subject header")
	}
	if !contains(s, "MIME-Version: 1.0\r\n") {
		t.Fatal("missing MIME-Version header")
	}
	if !contains(s, "Hello body") {
		t.Fatal("missing body")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
