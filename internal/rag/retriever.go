// internal/rag/retriever.go
package rag

import (
	"discord-rag-bot/internal/ai"
	"discord-rag-bot/internal/database"
	"discord-rag-bot/internal/models"
	"fmt"
	"strings"

	"github.com/pgvector/pgvector-go"
	"github.com/sashabaranov/go-openai"
)

type RAGRetriever struct {
	db *database.DB
	ai *ai.AIService
}

func NewRAGRetriever(db *database.DB, ai *ai.AIService) *RAGRetriever {
	return &RAGRetriever{db: db, ai: ai}
}

func (r *RAGRetriever) ProcessAndStoreMessage(msg *models.DiscordMessage) error {
	// Create embedding for the message
	embedding, err := r.ai.CreateEmbedding(msg.Content)
	if err != nil {
		return err
	}

	msg.Embedding = pgvector.NewVector(embedding)
	return r.db.StoreMessage(msg)
}

func (r *RAGRetriever) SearchRelevantContext(query, guildID string, limit int) (string, error) {
	// Create embedding for the query
	queryEmbedding, err := r.ai.CreateEmbedding(query)
	if err != nil {
		return "", err
	}

	// Search for similar messages
	messages, err := r.db.SearchSimilarMessages(queryEmbedding, guildID, limit)
	if err != nil {
		return "", err
	}

	// Also get some recent messages for temporal context
	recentMessages, err := r.db.GetRecentMessages(guildID, "", 3)
	if err != nil {
		return "", err
	}

	// Format context
	var contextBuilder strings.Builder

	contextBuilder.WriteString("=== RELEVANT PAST CONVERSATIONS ===\n")
	for _, msg := range messages {
		contextBuilder.WriteString(fmt.Sprintf("[%s] %s in #%s: %s\n",
			msg.Timestamp.Format("2006-01-02 15:04"),
			msg.Username,
			msg.ChannelName,
			msg.Content))
	}

	contextBuilder.WriteString("\n=== RECENT SERVER ACTIVITY ===\n")
	for _, msg := range recentMessages {
		contextBuilder.WriteString(fmt.Sprintf("[%s] %s: %s\n",
			msg.Timestamp.Format("15:04"),
			msg.Username,
			msg.Content))
	}

	return contextBuilder.String(), nil
}

func (r *RAGRetriever) GenerateResponse(userQuery, context, username, guildName string) (string, error) {
	systemPrompt := fmt.Sprintf(`You are a helpful AI assistant for the "%s" Discord server. 
You have access to the server's message history and can provide context-aware responses.

Current user: %s

Server Context:
%s

Instructions:
- Be conversational and friendly
- Reference relevant past conversations when helpful
- If you don't have relevant context, say so
- Keep responses concise but informative
- Use Discord-friendly formatting when appropriate`, guildName, username, context)

	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: userQuery,
		},
	}

	return r.ai.ChatCompletion(messages)
}
