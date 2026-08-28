package httpapi

import (
	"net/http"
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type ticketSummary struct {
	ID               string    `json:"id"`
	Number           int64     `json:"number"`
	UserID           string    `json:"userId"`
	RequesterName    string    `json:"requesterName"`
	RequesterEmail   string    `json:"requesterEmail"`
	Subject          string    `json:"subject"`
	Category         string    `json:"category"`
	Priority         string    `json:"priority"`
	Status           string    `json:"status"`
	EscalatedToHuman bool      `json:"escalatedToHuman"`
	MessageCount     int       `json:"messageCount"`
	LastMessage      string    `json:"lastMessage"`
	LastAuthorName   string    `json:"lastAuthorName"`
	LastReplyStaff   bool      `json:"lastReplyStaff"`
	LastMessageAt    time.Time `json:"lastMessageAt"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type ticketMessage struct {
	ID         string    `json:"id"`
	AuthorID   string    `json:"authorId"`
	AuthorName string    `json:"authorName"`
	StaffReply bool      `json:"staffReply"`
	AIReply    bool      `json:"aiReply"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (s *Server) listTickets(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	s.listTicketsFor(w, r, "WHERE t.user_id=$1", p.ID)
}

func (s *Server) adminListTickets(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && !validTicketStatus(status) {
		writeError(w, http.StatusBadRequest, "validation_failed", "invalid ticket status")
		return
	}
	if status == "" {
		s.listTicketsFor(w, r, "")
		return
	}
	s.listTicketsFor(w, r, "WHERE t.status=$1", status)
}

func (s *Server) listTicketsFor(w http.ResponseWriter, r *http.Request, where string, args ...any) {
	rows, err := s.db.Query(r.Context(), `SELECT t.id::text,t.number,t.user_id::text,u.display_name,u.email,t.subject,t.category,t.priority,t.status,t.escalated_to_human,
		(SELECT count(*) FROM support_ticket_messages m WHERE m.ticket_id=t.id),
		coalesce(latest.body,''),coalesce(latest.author_name,''),coalesce(latest.staff_reply,false),
		t.last_message_at,t.created_at,t.updated_at
		FROM support_tickets t JOIN users u ON u.id=t.user_id
		LEFT JOIN LATERAL (
			SELECT m.body,author.display_name AS author_name,m.staff_reply
			FROM support_ticket_messages m JOIN users author ON author.id=m.author_user_id
			WHERE m.ticket_id=t.id ORDER BY m.created_at DESC,m.id DESC LIMIT 1
		) latest ON true `+where+` ORDER BY
		CASE t.status WHEN 'open' THEN 0 WHEN 'in_progress' THEN 1 WHEN 'waiting_user' THEN 2 WHEN 'resolved' THEN 3 ELSE 4 END,
		CASE t.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END,t.last_message_at DESC LIMIT 200`, args...)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []ticketSummary{}
	for rows.Next() {
		var item ticketSummary
		if err = rows.Scan(&item.ID, &item.Number, &item.UserID, &item.RequesterName, &item.RequesterEmail, &item.Subject,
			&item.Category, &item.Priority, &item.Status, &item.EscalatedToHuman, &item.MessageCount, &item.LastMessage, &item.LastAuthorName,
			&item.LastReplyStaff, &item.LastMessageAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": items})
}

func (s *Server) createTicket(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		Subject  string `json:"subject"`
		Category string `json:"category"`
		Priority string `json:"priority"`
		Body     string `json:"body"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	q.Subject, q.Body = strings.TrimSpace(q.Subject), strings.TrimSpace(q.Body)
	if q.Priority == "" {
		q.Priority = "normal"
	}
	if utf8.RuneCountInString(q.Subject) < 2 || utf8.RuneCountInString(q.Subject) > 160 ||
		!validTicketCategory(q.Category) || !validTicketPriority(q.Priority) || !validTicketBody(q.Body) {
		writeError(w, http.StatusBadRequest, "validation_failed", "请填写完整的工单标题、类型、优先级和问题描述")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var id string
	var number int64
	if err = tx.QueryRow(r.Context(), `INSERT INTO support_tickets(user_id,subject,category,priority) VALUES($1,$2,$3,$4) RETURNING id::text,number`,
		p.ID, q.Subject, q.Category, q.Priority).Scan(&id, &number); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO support_ticket_messages(ticket_id,author_user_id,body) VALUES($1,$2,$3)`, id, p.ID, q.Body); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,$2,'ticket.create','support_ticket',$3,$4,jsonb_build_object('number',$5::bigint,'category',$6::text,'priority',$7::text))`,
		p.auditActorID(), p.ID, id, requestID(r.Context()), number, q.Category, q.Priority); err != nil {
		s.internalError(w, err)
		return
	}
if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	
	// AI trigger
	go func() {
		cfg, _ := getAISupportSettingsConfig(context.Background(), s)
		if cfg.Enabled {
			s.processAITicketReply(context.Background(), id, nil, q.Subject+"\n\n"+q.Body)
		}
	}()

	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "number": number, "status": "open"})
}

func (s *Server) getTicket(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	s.ticketDetail(w, r, false, p.ID)
}

func (s *Server) adminGetTicket(w http.ResponseWriter, r *http.Request) {
	s.ticketDetail(w, r, true, "")
}

func (s *Server) ticketDetail(w http.ResponseWriter, r *http.Request, admin bool, userID string) {
	id := r.PathValue("ticketID")
	if !validUUID(id) {
		writeError(w, http.StatusBadRequest, "validation_failed", "ticketID must be a UUID")
		return
	}
	query := `SELECT t.id::text,t.number,t.user_id::text,u.display_name,u.email,t.subject,t.category,t.priority,t.status,t.escalated_to_human,
		(SELECT count(*) FROM support_ticket_messages m WHERE m.ticket_id=t.id),
		coalesce(latest.body,''),coalesce(latest.author_name,''),coalesce(latest.staff_reply,false),
		t.last_message_at,t.created_at,t.updated_at
		FROM support_tickets t JOIN users u ON u.id=t.user_id
		LEFT JOIN LATERAL (
			SELECT m.body,author.display_name AS author_name,m.staff_reply
			FROM support_ticket_messages m JOIN users author ON author.id=m.author_user_id
			WHERE m.ticket_id=t.id ORDER BY m.created_at DESC,m.id DESC LIMIT 1
		) latest ON true WHERE t.id=$1`
	args := []any{id}
	if !admin {
		query += " AND t.user_id=$2"
		args = append(args, userID)
	}
	var item ticketSummary
	err := s.db.QueryRow(r.Context(), query, args...).Scan(&item.ID, &item.Number, &item.UserID, &item.RequesterName, &item.RequesterEmail,
		&item.Subject, &item.Category, &item.Priority, &item.Status, &item.EscalatedToHuman, &item.MessageCount, &item.LastMessage, &item.LastAuthorName,
		&item.LastReplyStaff, &item.LastMessageAt, &item.CreatedAt, &item.UpdatedAt)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "ticket_not_found", "ticket not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT m.id::text,m.author_user_id::text,u.display_name,m.staff_reply,m.ai_reply,m.body,m.created_at
		FROM support_ticket_messages m JOIN users u ON u.id=m.author_user_id WHERE m.ticket_id=$1 ORDER BY m.created_at,m.id`, id)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	messages := []ticketMessage{}
	for rows.Next() {
		var message ticketMessage
		if err = rows.Scan(&message.ID, &message.AuthorID, &message.AuthorName, &message.StaffReply, &message.AIReply, &message.Body, &message.CreatedAt); err != nil {
			s.internalError(w, err)
			return
		}
		messages = append(messages, message)
	}
	if err = rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": item, "messages": messages})
}

func (s *Server) replyTicket(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	s.addTicketReply(w, r, p, false)
}

func (s *Server) adminReplyTicket(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	s.addTicketReply(w, r, p, true)
}

func (s *Server) addTicketReply(w http.ResponseWriter, r *http.Request, p principal, staff bool) {
	id := r.PathValue("ticketID")
	var q struct {
		Body string `json:"body"`
	}
	if !validUUID(id) {
		writeError(w, http.StatusBadRequest, "validation_failed", "ticketID must be a UUID")
		return
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	q.Body = strings.TrimSpace(q.Body)
	if !validTicketBody(q.Body) {
		writeError(w, http.StatusBadRequest, "validation_failed", "回复内容必须为 1 到 10000 个字符")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var ownerID, status string
	var escalatedToHuman bool
	if err = tx.QueryRow(r.Context(), `SELECT user_id::text,status,escalated_to_human FROM support_tickets WHERE id=$1 FOR UPDATE`, id).Scan(&ownerID, &status, &escalatedToHuman); err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "ticket_not_found", "ticket not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if !staff && ownerID != p.ID {
		writeError(w, http.StatusNotFound, "ticket_not_found", "ticket not found")
		return
	}
	if status == "closed" {
		writeError(w, http.StatusConflict, "ticket_closed", "closed tickets cannot receive replies")
		return
	}
	var messageID string
	if err = tx.QueryRow(r.Context(), `INSERT INTO support_ticket_messages(ticket_id,author_user_id,body,staff_reply) VALUES($1,$2,$3,$4) RETURNING id::text`,
		id, p.ID, q.Body, staff).Scan(&messageID); err != nil {
		s.internalError(w, err)
		return
	}
	nextStatus := status
	if staff {
		nextStatus = "waiting_user"
	} else if status == "waiting_user" || status == "resolved" {
		nextStatus = "open"
	}
	if _, err = tx.Exec(r.Context(), `UPDATE support_tickets SET status=$2,last_message_at=now(),updated_at=now() WHERE id=$1`, id, nextStatus); err != nil {
		s.internalError(w, err)
		return
	}
	action := "ticket.user.reply"
	if staff {
		action = "ticket.staff.reply"
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,$2,$3,'support_ticket',$4,$5,jsonb_build_object('message_id',$6::text,'status',$7::text))`,
		p.auditActorID(), ownerID, action, id, requestID(r.Context()), messageID, nextStatus); err != nil {
		s.internalError(w, err)
		return
	}
if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}

	// AI trigger
	if !staff && !escalatedToHuman {
		go func() {
			cfg, _ := getAISupportSettingsConfig(context.Background(), s)
			if cfg.Enabled {
				history, _ := s.getTicketHistory(context.Background(), id)
				s.processAITicketReply(context.Background(), id, history, q.Body)
			}
		}()
	}

	writeJSON(w, http.StatusCreated, map[string]any{"id": messageID, "status": nextStatus})
}

func (s *Server) closeTicket(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	id := r.PathValue("ticketID")
	if !validUUID(id) {
		writeError(w, http.StatusBadRequest, "validation_failed", "ticketID must be a UUID")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	result, err := tx.Exec(r.Context(), `UPDATE support_tickets SET status='closed',updated_at=now() WHERE id=$1 AND user_id=$2 AND status<>'closed'`, id, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "ticket_not_found", "ticket not found or already closed")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id)
		VALUES($1,$2,'ticket.close','support_ticket',$3,$4)`, p.auditActorID(), p.ID, id, requestID(r.Context())); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "status": "closed"})
}

func (s *Server) adminUpdateTicketStatus(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	id := r.PathValue("ticketID")
	var q struct {
		Status string `json:"status"`
	}
	if !validUUID(id) {
		writeError(w, http.StatusBadRequest, "validation_failed", "ticketID must be a UUID")
		return
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !validTicketStatus(q.Status) {
		writeError(w, http.StatusBadRequest, "validation_failed", "invalid ticket status")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var ownerID, previous string
	if err = tx.QueryRow(r.Context(), `SELECT user_id::text,status FROM support_tickets WHERE id=$1 FOR UPDATE`, id).Scan(&ownerID, &previous); err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "ticket_not_found", "ticket not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE support_tickets SET status=$2,updated_at=now() WHERE id=$1`, id, q.Status); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,$2,'ticket.status.update','support_ticket',$3,$4,jsonb_build_object('from',$5::text,'to',$6::text))`, p.auditActorID(), ownerID, id, requestID(r.Context()), previous, q.Status); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "status": q.Status, "previous": previous})
}

func validTicketCategory(value string) bool {
	return value == "deployment" || value == "billing" || value == "account" || value == "product" || value == "other"
}
func validTicketPriority(value string) bool {
	return value == "low" || value == "normal" || value == "high" || value == "urgent"
}
func validTicketStatus(value string) bool {
	return value == "open" || value == "in_progress" || value == "waiting_user" || value == "resolved" || value == "closed"
}
func validTicketBody(value string) bool {
	length := utf8.RuneCountInString(value)
	return length >= 1 && length <= 10000
}

func (s *Server) escalateTicket(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	id := r.PathValue("ticketID")
	if !validUUID(id) {
		writeError(w, http.StatusBadRequest, "validation_failed", "ticketID must be a UUID")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	
	var ownerID string
	if err = tx.QueryRow(r.Context(), `SELECT user_id::text FROM support_tickets WHERE id=$1 FOR UPDATE`, id).Scan(&ownerID); err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "ticket_not_found", "ticket not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if ownerID != p.ID {
		writeError(w, http.StatusNotFound, "ticket_not_found", "ticket not found")
		return
	}
	
	if _, err = tx.Exec(r.Context(), `UPDATE support_tickets SET escalated_to_human=true,updated_at=now() WHERE id=$1`, id); err != nil {
		s.internalError(w, err)
		return
	}
	
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id)
		VALUES($1,$2,'ticket.escalate','support_ticket',$3,$4)`, p.auditActorID(), ownerID, id, requestID(r.Context())); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"escalated": true})
}
