package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func processBalanceEmailOne(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	// A Worker restart can leave a claimed message in sending. Requeue it
	// after a bounded lease so delivery is eventually retried.
	_, _ = tx.Exec(ctx, `UPDATE user_notifications SET email_status='queued',email_next_attempt_at=now() WHERE kind='low_balance' AND email_status='sending' AND email_attempts<3 AND email_sent_at IS NULL AND created_at < now()-interval '10 minutes'`)
	var id, email, title, content string
	err = tx.QueryRow(ctx, `UPDATE user_notifications n SET email_status='sending',email_attempts=email_attempts+1 WHERE n.id=(SELECT n2.id FROM user_notifications n2 WHERE n2.kind='low_balance' AND n2.email_status IN ('queued','failed') AND n2.email_attempts<3 AND n2.email_next_attempt_at<=now() ORDER BY n2.created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING n.id,(SELECT email FROM users WHERE id=n.user_id),n.title,n.content`).Scan(&id, &email, &title, &content)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		return
	}
	if err = tx.Commit(ctx); err != nil {
		return
	}
	var enabled bool
	var host, user, password, from, name, mode string
	var port int
	err = db.QueryRow(ctx, `SELECT enabled,host,port,username,password,from_email,from_name,tls_mode FROM smtp_settings WHERE singleton`).Scan(&enabled, &host, &port, &user, &password, &from, &name, &mode)
	if err == nil && (!enabled || host == "" || from == "") {
		err = fmt.Errorf("SMTP is not configured")
	}
	if err == nil && password != "" {
		password, err = secrets.Decrypt("smtp.password", password)
	}
	if err == nil {
		body := fmt.Sprintf("From: %s <%s>\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", name, from, email, title, content)
		err = sendWorkerSMTP(host, port, user, password, from, email, mode, []byte(body))
	}
	if err != nil {
		_, _ = db.Exec(ctx, `UPDATE user_notifications SET email_status='failed',email_last_error=$2,email_next_attempt_at=now()+interval '15 minutes' WHERE id=$1`, id, err.Error())
		_, _ = db.Exec(ctx, `INSERT INTO audit_logs(action,resource_type,resource_id,request_id,metadata) VALUES('balance-alert.email.failed','user_notification',$1,$2,jsonb_build_object('error',$3::text))`, id, "worker/balance-email/"+id, err.Error())
		logger.Warn("balance alert email failed", "notification", id, "error", err)
		return
	}
	_, _ = db.Exec(ctx, `UPDATE user_notifications SET email_status='sent',email_sent_at=now(),email_last_error='' WHERE id=$1`, id)
	_, _ = db.Exec(ctx, `INSERT INTO audit_logs(action,resource_type,resource_id,request_id,metadata) VALUES('balance-alert.email.sent','user_notification',$1,$2,'{}')`, id, "worker/balance-email/"+id)
}

func sendWorkerSMTP(host string, port int, user, password, from, to, mode string, message []byte) error {
	address := net.JoinHostPort(host, fmt.Sprint(port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	var client *smtp.Client
	var err error
	if mode == "tls" {
		var connection *tls.Conn
		connection, err = tls.DialWithDialer(dialer, "tcp", address, config)
		if err == nil {
			client, err = smtp.NewClient(connection, host)
		}
	} else {
		var connection net.Conn
		connection, err = dialer.Dial("tcp", address)
		if err == nil {
			client, err = smtp.NewClient(connection, host)
		}
		if err == nil && mode == "starttls" {
			err = client.StartTLS(config)
		}
	}
	if err != nil {
		return err
	}
	defer client.Close()
	if strings.TrimSpace(user) != "" {
		if err = client.Auth(smtp.PlainAuth("", user, password, host)); err != nil {
			return err
		}
	}
	if err = client.Mail(from); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write(message); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
