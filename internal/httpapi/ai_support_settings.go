package httpapi

import (
	"context"
	"net/http"
)

type aiSupportSettings struct {
	Enabled       bool   `json:"enabled"`
	Provider      string `json:"provider"`
	BaseURL       string `json:"baseUrl"`
	APIKey        string `json:"apiKey"`
	ModelName     string `json:"modelName"`
	SystemPrompt  string `json:"systemPrompt"`
	KnowledgeBase string `json:"knowledgeBase"`
}

func (s *Server) getAISupportSettings(w http.ResponseWriter, r *http.Request) {
	var q aiSupportSettings
	if err := s.db.QueryRow(r.Context(), "SELECT enabled,provider,base_url,api_key,model_name,system_prompt,knowledge_base FROM ai_support_settings WHERE singleton").Scan(&q.Enabled, &q.Provider, &q.BaseURL, &q.APIKey, &q.ModelName, &q.SystemPrompt, &q.KnowledgeBase); err != nil {
		s.internalError(w, err)
		return
	}
	// Redact API Key in GET response for security
	if q.APIKey != "" {
		q.APIKey = "********"
	}
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) updateAISupportSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q aiSupportSettings
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}

	// Fetch current API key if the user submits the redacted mask
	if q.APIKey == "********" {
		if err := s.db.QueryRow(r.Context(), "SELECT api_key FROM ai_support_settings WHERE singleton").Scan(&q.APIKey); err != nil {
			s.internalError(w, err)
			return
		}
	}

	if _, err := s.db.Exec(r.Context(), `UPDATE ai_support_settings SET enabled=$1,provider=$2,base_url=$3,api_key=$4,model_name=$5,system_prompt=$6,knowledge_base=$7 WHERE singleton`,
		q.Enabled, q.Provider, q.BaseURL, q.APIKey, q.ModelName, q.SystemPrompt, q.KnowledgeBase); err != nil {
		s.internalError(w, err)
		return
	}

	if _, err := s.db.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'ai-support.settings.update','ai_support_settings','singleton',$2,jsonb_build_object('enabled',$3::boolean,'provider',$4::text))`, p.ID, requestID(r.Context()), q.Enabled, q.Provider); err != nil {
		s.internalError(w, err)
		return
	}

	q.APIKey = "********"
	writeJSON(w, http.StatusOK, q)
}

func getAISupportSettingsConfig(ctx context.Context, s *Server) (aiSupportSettings, error) {
	var q aiSupportSettings
	err := s.db.QueryRow(ctx, "SELECT enabled,provider,base_url,api_key,model_name,system_prompt,knowledge_base FROM ai_support_settings WHERE singleton").
		Scan(&q.Enabled, &q.Provider, &q.BaseURL, &q.APIKey, &q.ModelName, &q.SystemPrompt, &q.KnowledgeBase)
	return q, err
}

func (s *Server) testAISupportSettings(w http.ResponseWriter, r *http.Request) {
	var q aiSupportSettings
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}

	if q.APIKey == "********" {
		if err := s.db.QueryRow(r.Context(), "SELECT api_key FROM ai_support_settings WHERE singleton").Scan(&q.APIKey); err != nil {
			s.internalError(w, err)
			return
		}
	}

	messages := []aiMessage{
		{Role: "user", Content: "Hello! Please reply exactly with the word 'OK' to confirm you are online."},
	}

	resp, err := callAIModel(r.Context(), q, messages)
	if err != nil {
		writeError(w, 400, "test_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "连接成功，模型回复: " + resp})
}
