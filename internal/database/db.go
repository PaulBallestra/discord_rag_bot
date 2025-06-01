// internal/database/db.go - Updated SearchSimilarMessages method
package database

import (
	"discord-rag-bot/internal/models"
	"fmt"

	"github.com/pgvector/pgvector-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type DB struct {
	*gorm.DB
}

func NewDB(host, user, password, dbname string, port int) (*DB, error) {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=disable",
		host, user, password, dbname, port)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// Enable pgvector extension
	db.Exec("CREATE EXTENSION IF NOT EXISTS vector")

	// Auto migrate
	err = db.AutoMigrate(
		&models.DiscordMessage{},
		&models.BotInteraction{},
		&models.ConversationContext{},
	)
	if err != nil {
		return nil, err
	}

	return &DB{db}, nil
}

// Fixed method signature and implementation
func (db *DB) SearchSimilarMessages(embedding []float32, guildID string, limit int) ([]models.DiscordMessage, error) {
	var messages []models.DiscordMessage

	// Convert to pgvector format
	vector := pgvector.NewVector(embedding)

	// Use raw SQL for vector similarity search
	query := `
        SELECT id, message_id, content, author, username, channel_id, channel_name, 
               guild_id, guild_name, timestamp, embedding, created_at
        FROM discord_messages 
        WHERE guild_id = ? 
        ORDER BY embedding <-> ? 
        LIMIT ?`

	err := db.Raw(query, guildID, vector, limit).Scan(&messages).Error
	return messages, err
}

// Alternative method using GORM's native methods (if the above doesn't work)
func (db *DB) SearchSimilarMessagesAlternative(embedding []float32, guildID string, limit int) ([]models.DiscordMessage, error) {
	var messages []models.DiscordMessage

	// Convert to pgvector format
	vector := pgvector.NewVector(embedding)

	// Use DB.Raw for the vector operation
	subQuery := db.Model(&models.DiscordMessage{}).
		Where("guild_id = ?", guildID).
		Select("*, embedding <-> ? as distance", vector).
		Order("distance").
		Limit(limit)

	err := subQuery.Find(&messages).Error
	return messages, err
}

// Method to store message with embedding
func (db *DB) CreateMessageWithEmbedding(message *models.DiscordMessage, embedding []float32) error {
	message.Embedding = pgvector.NewVector(embedding)
	return db.Create(message).Error
}
