package httpapi

import (
	"context"
		"log/slog"
	"strings"
)

func (s *Server) processAITicketReply(ctx context.Context, ticketID string, ticketHistory []aiMessage, latestMessage string) {
	cfg, err := getAISupportSettingsConfig(ctx, s)
	if err != nil {
		slog.Error("failed to get AI config", "error", err)
		return
	}
	if !cfg.Enabled {
		return
	}

	messages := []aiMessage{
		{Role: "system", Content: cfg.SystemPrompt + "\n\nKnowledge Base:\n" + cfg.KnowledgeBase},
	}
	messages = append(messages, ticketHistory...)
	messages = append(messages, aiMessage{Role: "user", Content: latestMessage})

	replyContent, err := callAIModel(ctx, cfg, messages)
	if err != nil {
		slog.Error("AI ticket reply failed", "ticket", ticketID, "error", err)
		return
	}

	replyContent = strings.TrimSpace(replyContent)
	if replyContent == "" {
		return
	}

	// Insert the AI reply into the ticket
	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.Error("failed to start tx for AI reply", "error", err)
		return
	}
	defer tx.Rollback(ctx)

	var messageID string
	if err := tx.QueryRow(ctx, `INSERT INTO support_ticket_messages(ticket_id,author_user_id,body,staff_reply,ai_reply) VALUES($1, (SELECT id FROM users WHERE role='super_admin' ORDER BY created_at LIMIT 1), $2, true, true) RETURNING id::text`, ticketID, replyContent).Scan(&messageID); err != nil {
		slog.Error("failed to insert AI reply", "error", err)
		return
	}

	if _, err := tx.Exec(ctx, `UPDATE support_tickets SET status='waiting_user',last_message_at=now(),updated_at=now() WHERE id=$1`, ticketID); err != nil {
		slog.Error("failed to update ticket status for AI reply", "error", err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("failed to commit AI reply", "error", err)
	}
}

// getTicketHistory fetches recent messages to build context
func (s *Server) getTicketHistory(ctx context.Context, ticketID string) ([]aiMessage, error) {
	rows, err := s.db.Query(ctx, `SELECT body, staff_reply, ai_reply FROM support_ticket_messages WHERE ticket_id=$1 ORDER BY created_at ASC LIMIT 10`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []aiMessage
	for rows.Next() {
		var body string
		var staffReply, aiReply bool
		if err := rows.Scan(&body, &staffReply, &aiReply); err != nil {
			return nil, err
		}
		role := "user"
		if staffReply {
			role = "assistant"
		}
		history = append(history, aiMessage{Role: role, Content: body})
	}
	return history, rows.Err()
}
