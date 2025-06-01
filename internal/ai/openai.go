// internal/ai/openai.go
package ai

import (
	"context"
	"io"

	"github.com/sashabaranov/go-openai"
)

type AIService struct {
	client *openai.Client
}

func NewAIService(apiKey string) *AIService {
	return &AIService{
		client: openai.NewClient(apiKey),
	}
}

func (ai *AIService) CreateEmbedding(text string) ([]float32, error) {
	resp, err := ai.client.CreateEmbeddings(context.Background(), openai.EmbeddingRequest{
		Input: []string{text},
		Model: openai.AdaEmbeddingV2,
	})
	if err != nil {
		return nil, err
	}

	return resp.Data[0].Embedding, nil
}

func (ai *AIService) ChatCompletion(messages []openai.ChatCompletionMessage) (string, error) {
	resp, err := ai.client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:       openai.GPT4,
		Messages:    messages,
		MaxTokens:   500,
		Temperature: 0.7,
	})
	if err != nil {
		return "", err
	}

	return resp.Choices[0].Message.Content, nil
}

func (ai *AIService) SpeechToText(audioFile io.Reader) (string, error) {
	req := openai.AudioRequest{
		Model:  openai.Whisper1,
		Reader: audioFile,
	}

	resp, err := ai.client.CreateTranscription(context.Background(), req)
	if err != nil {
		return "", err
	}

	return resp.Text, nil
}

func (ai *AIService) TextToSpeech(text string) ([]byte, error) {
	req := openai.CreateSpeechRequest{
		Model:          openai.TTSModel1,
		Input:          text,
		Voice:          openai.VoiceAlloy, // You can change to: Alloy, Echo, Fable, Onyx, Nova, Shimmer
		ResponseFormat: openai.SpeechResponseFormatMp3,
		Speed:          1.0,
	}

	resp, err := ai.client.CreateSpeech(context.Background(), req)
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	return io.ReadAll(resp)
}
