// internal/ai/embeddings.go
package ai

import (
	"context"
	"fmt"

	"github.com/sashabaranov/go-openai"
)

// GenerateEmbeddings creates vector embeddings for multiple texts
func (ai *AIService) GenerateEmbeddings(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("no texts provided for embedding")
	}

	req := openai.EmbeddingRequest{
		Input: texts,
		Model: openai.AdaEmbeddingV2,
	}

	resp, err := ai.client.CreateEmbeddings(context.Background(), req)
	if err != nil {
		return nil, fmt.Errorf("failed to create embeddings: %v", err)
	}

	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("embedding count mismatch: got %d, expected %d", len(resp.Data), len(texts))
	}

	embeddings := make([][]float32, len(resp.Data))
	for i, data := range resp.Data {
		embeddings[i] = data.Embedding
	}

	return embeddings, nil
}

// CalculateCosineSimilarity calculates similarity between two embeddings
func (ai *AIService) CalculateCosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (normA * normB)
}
