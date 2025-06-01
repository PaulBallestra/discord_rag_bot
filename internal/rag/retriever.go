// internal/rag/retriever.go - Updated with exported AI field
package rag

import (
	"discord-rag-bot/internal/ai"
	"discord-rag-bot/internal/database"
	"discord-rag-bot/internal/models"
	"fmt"
	"strings"

	"github.com/pgvector/pgvector-go"
)

type RAGRetriever struct {
	db *database.DB
	AI *ai.AIService // Export this field (capital A)
}

func NewRAGRetriever(db *database.DB, aiService *ai.AIService) *RAGRetriever {
	return &RAGRetriever{
		db: db,
		AI: aiService, // Use exported field
	}
}

func (r *RAGRetriever) SearchRelevantContext(query string, guildID string, limit int) (string, error) {
	// Generate embedding for the query
	embedding, err := r.AI.GenerateEmbedding(query)
	if err != nil {
		return "", fmt.Errorf("failed to generate query embedding: %v", err)
	}

	// Search for similar messages
	messages, err := r.db.SearchSimilarMessages(embedding, guildID, limit)
	if err != nil {
		return "", fmt.Errorf("failed to search similar messages: %v", err)
	}

	// Build context string
	var contextParts []string
	for _, msg := range messages {
		contextParts = append(contextParts, fmt.Sprintf("[%s] %s: %s",
			msg.ChannelName, msg.Username, msg.Content))
	}

	return strings.Join(contextParts, "\n"), nil
}

func (r *RAGRetriever) GenerateResponse(query, context, username, guildName string) (string, error) {
	systemPrompt := fmt.Sprintf(`You are a helpful Discord bot assistant for the "%s" server. 
You have access to the server's message history and should provide helpful, contextual responses.

Current conversation context from server messages:
%s

Guidelines:
- Be friendly and conversational
- Reference relevant context when helpful
- Keep responses concise but informative
- Adapt your tone to match the server's culture
- If you don't have relevant context, say so politely`, guildName, context)

	userPrompt := fmt.Sprintf("%s asked: %s", username, query)

	response, err := r.AI.GenerateResponse(systemPrompt, userPrompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate AI response: %v", err)
	}

	return response.(string), nil
}

// StoreMessageWithEmbedding stores a message and generates its embedding
func (r *RAGRetriever) StoreMessageWithEmbedding(message *models.DiscordMessage) error {
	// Generate embedding for the message content
	if message.Content != "" {
		embedding, err := r.AI.GenerateEmbedding(message.Content)
		if err != nil {
			return fmt.Errorf("failed to generate embedding: %v", err)
		}

		// Convert to pgvector format
		message.Embedding = pgvector.NewVector(embedding)
	}

	return r.db.Create(message).Error
}
