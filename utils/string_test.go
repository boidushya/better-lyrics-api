package utils

import (
	"testing"
)

func TestCompressAndDecompressString(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{
			name: "Short string",
			text: "Hello, world!",
		},
		{
			name: "Longer JSON string",
			text: `{"name":"John Doe","age":30,"city":"New York"}`,
		},
		{
			name: "Empty string",
			text: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := CompressString(tt.text)
			if err != nil {
				t.Fatalf("CompressString error: %v", err)
			}

			decompressed, err := DecompressString(compressed)
			if err != nil {
				t.Fatalf("DecompressString error: %v", err)
			}

			if decompressed != tt.text {
				t.Errorf("Expected decompressed string %q, got %q", tt.text, decompressed)
			}
		})
	}
}

func TestInvalidBase64Decompression(t *testing.T) {
	invalidInput := "invalid_base64_string"

	_, err := DecompressString(invalidInput)
	if err == nil {
		t.Error("Expected error when decompressing invalid base64 string")
	}
}
