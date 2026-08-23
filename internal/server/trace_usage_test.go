package server

import "testing"

func TestExplicitUsageCacheAccounting(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		creation   int64
		read       int64
		cacheInput int64
	}{
		{
			name:       "anthropic",
			body:       `{"usage":{"input_tokens":3,"output_tokens":4,"cache_creation_input_tokens":2,"cache_read_input_tokens":5}}`,
			creation:   2,
			read:       5,
			cacheInput: 10,
		},
		{
			name:       "openai chat nested",
			body:       `{"response":{"usage":{"prompt_tokens":11,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":5}}}}`,
			read:       5,
			cacheInput: 11,
		},
		{
			name:       "without cache accounting",
			body:       `{"usage":{"input_tokens":3,"output_tokens":4}}`,
			cacheInput: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage, ok := explicitUsage([]byte(tt.body))
			if !ok {
				t.Fatal("usage was not detected")
			}
			if usage.CacheCreationInputTokens != tt.creation || usage.CacheReadInputTokens != tt.read || usage.CacheInputTokens != tt.cacheInput {
				t.Fatalf("usage = %+v", usage)
			}
		})
	}
}
