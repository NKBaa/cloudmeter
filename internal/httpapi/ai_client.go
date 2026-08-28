package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type aiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aiChatRequest struct {
	Model    string      `json:"model"`
	Messages []aiMessage `json:"messages"`
}

type aiChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func callAIModel(ctx context.Context, cfg aiSupportSettings, messages []aiMessage) (string, error) {
	if cfg.BaseURL == "" {
		return "", fmt.Errorf("AI support base URL is empty")
	}

	reqBody := aiChatRequest{
		Model:    cfg.ModelName,
		Messages: messages,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("AI API error: %d %s", resp.StatusCode, string(respBody))
	}

	var resData aiChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&resData); err != nil {
		return "", err
	}

	if len(resData.Choices) == 0 {
		return "", fmt.Errorf("AI API returned no choices")
	}

	return resData.Choices[0].Message.Content, nil
}
