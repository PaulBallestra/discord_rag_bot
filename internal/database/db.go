// internal/database/db.go
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
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=disable TimeZone=UTC",
		host, user, password, dbname, port)

	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// Enable pgvector extension
	if err := gormDB.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return nil, err
	}

	// Auto migrate
	if err := gormDB.AutoMigrate(
		&models.DiscordMessage{},
		&models.BotInteraction{},
		&models.ConversationContext{},
	); err != nil {
		return nil, err
	}

	return &DB{gormDB}, nil
}

func (db *DB) StoreMessage(msg *models.DiscordMessage) error {
	return db.FirstOrCreate(msg, models.DiscordMessage{MessageID: msg.MessageID}).Error
}

func (db *DB) SearchSimilarMessages(embedding []float32, guildID string, limit int) ([]models.DiscordMessage, error) {
	var messages []models.DiscordMessage

	// Convert to pgvector format
	vector := pgvector.NewVector(embedding)

	err := db.Where("guild_id = ?", guildID).
		Order("embedding <-> ?", vector).
		Limit(limit).
		Find(&messages).Error

	return messages, err
}

func (db *DB) GetRecentMessages(guildID, channelID string, limit int) ([]models.DiscordMessage, error) {
	var messages []models.DiscordMessage

	query := db.Where("guild_id = ?", guildID)
	if channelID != "" {
		query = query.Where("channel_id = ?", channelID)
	}

	err := query.Order("timestamp DESC").Limit(limit).Find(&messages).Error
	return messages, err
}
