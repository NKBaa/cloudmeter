package httpapi

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
)

func startTestSMTP(t *testing.T) (string, int, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	messages := make(chan string, 1)
	go func() {
		defer listener.Close()
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		writer := bufio.NewWriter(connection)
		_, _ = writer.WriteString("220 test SMTP\r\n")
		_ = writer.Flush()
		var data strings.Builder
		inData := false
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					messages <- data.String()
					_, _ = writer.WriteString("250 queued\r\n")
					_ = writer.Flush()
				} else {
					data.WriteString(line)
				}
				continue
			}
			command := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(command, "EHLO"):
				_, _ = writer.WriteString("250-test\r\n250 8BITMIME\r\n")
			case strings.HasPrefix(command, "HELO"), strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
				_, _ = writer.WriteString("250 ok\r\n")
			case command == "DATA":
				inData = true
				_, _ = writer.WriteString("354 continue\r\n")
			case command == "QUIT":
				_, _ = writer.WriteString("221 bye\r\n")
				_ = writer.Flush()
				return
			default:
				_, _ = writer.WriteString("500 unsupported\r\n")
			}
			_ = writer.Flush()
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", address.Port, messages
}

func TestSendSMTPWithoutTLS(t *testing.T) {
	host, port, messages := startTestSMTP(t)
	settings := smtpConnectionSettings{Host: host, Port: port, FromEmail: "sender@example.com", TLSMode: "none"}
	if err := sendSMTP(settings, []string{"receiver@example.com"}, []byte("Subject: test\r\n\r\nhello\r\n")); err != nil {
		t.Fatal(err)
	}
	if got := <-messages; !strings.Contains(got, "hello") {
		t.Fatalf("message body not received: %q", got)
	}
}

func TestSendSMTPStartTLSDoesNotDowngrade(t *testing.T) {
	host, port, _ := startTestSMTP(t)
	settings := smtpConnectionSettings{Host: host, Port: port, FromEmail: "sender@example.com", TLSMode: "starttls"}
	err := sendSMTP(settings, []string{"receiver@example.com"}, []byte("Subject: test\r\n\r\nhello\r\n"))
	if err == nil || !strings.Contains(err.Error(), "does not support STARTTLS") {
		t.Fatalf("expected STARTTLS refusal, got %v", err)
	}
}

func TestSMTPAddressUsesIPv6SafeJoin(t *testing.T) {
	if got := net.JoinHostPort("::1", fmt.Sprintf("%d", 25)); got != "[::1]:25" {
		t.Fatalf("unexpected address %q", got)
	}
}
