// internal/models/models.go
package models

import (
	"time"

	"github.com/pgvector/pgvector-go"
)

type DiscordMessage struct {
	ID          uint   `gorm:"primaryKey"`
	MessageID   string `gorm:"uniqueIndex;not null"`
	Content     string `gorm:"type:text"`
	Author      string `gorm:"not null"`
	Username    string `gorm:"not null"`
	ChannelID   string `gorm:"not null"`
	ChannelName string
	GuildID     string `gorm:"not null"`
	GuildName   string
	Timestamp   time.Time       `gorm:"not null"`
	Embedding   pgvector.Vector `gorm:"type:vector(1536)"` // OpenAI embedding size
	CreatedAt   time.Time
}

type BotInteraction struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    string    `gorm:"not null"`
	Username  string    `gorm:"not null"`
	Query     string    `gorm:"type:text"`
	Response  string    `gorm:"type:text"`
	ChannelID string    `gorm:"not null"`
	GuildID   string    `gorm:"not null"`
	IsVoice   bool      `gorm:"default:false"`
	Timestamp time.Time `gorm:"not null"`
	CreatedAt time.Time
}

type ConversationContext struct {
	ID        uint   `gorm:"primaryKey"`
	UserID    string `gorm:"not null"`
	ChannelID string `gorm:"not null"`
	Context   string `gorm:"type:jsonb"`
	UpdatedAt time.Time
}
