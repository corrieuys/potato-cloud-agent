package container

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectLanguage_NodeJS(t *testing.T) {
	t.Logf("Testing Node.js language detection")

	gen := NewGenerator(3000, 3100)
	tempDir := t.TempDir()

	// Create package.json
	pkgJSON := `{"name": "test", "version": "1.0.0"}`
	if err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	lang := gen.DetectLanguage(tempDir)
	if lang != "nodejs" {
		t.Errorf("Expected language 'nodejs', got '%s'", lang)
	}

	t.Logf("✓ Correctly detected Node.js")
}

func TestDetectLanguage_Go(t *testing.T) {
	t.Logf("Testing Go language detection")

	gen := NewGenerator(3000, 3100)
	tempDir := t.TempDir()

	// Create go.mod
	goMod := `module test

go 1.21`
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	lang := gen.DetectLanguage(tempDir)
	if lang != "golang" {
		t.Errorf("Expected language 'golang', got '%s'", lang)
	}

	t.Logf("✓ Correctly detected Go")
}

func TestDetectLanguage_Python(t *testing.T) {
	t.Logf("Testing Python language detection")

	gen := NewGenerator(3000, 3100)
	tempDir := t.TempDir()

	// Create requirements.txt
	reqs := `flask==2.0.0`
	if err := os.WriteFile(filepath.Join(tempDir, "requirements.txt"), []byte(reqs), 0644); err != nil {
		t.Fatalf("Failed to create requirements.txt: %v", err)
	}

	lang := gen.DetectLanguage(tempDir)
	if lang != "python" {
		t.Errorf("Expected language 'python', got '%s'", lang)
	}

	t.Logf("✓ Correctly detected Python")
}

func TestDetectLanguage_Generic(t *testing.T) {
	t.Logf("Testing generic language detection fallback")

	gen := NewGenerator(3000, 3100)
	tempDir := t.TempDir()

	// No recognizable files
	lang := gen.DetectLanguage(tempDir)
	if lang != "generic" {
		t.Errorf("Expected language 'generic', got '%s'", lang)
	}

	t.Logf("✓ Correctly fell back to generic")
}

func TestGenerateDockerfile_NodeJS(t *testing.T) {
	t.Logf("Testing Node.js Dockerfile generation")

	gen := NewGenerator(3000, 3100)
	tempDir := t.TempDir()

	envVars := map[string]string{
		"NODE_ENV": "production",
	}

	content, err := gen.GenerateDockerfile("nodejs", "", 3000, envVars, "npm run build", "npm start", tempDir)
	if err != nil {
		t.Fatalf("Failed to generate Dockerfile: %v", err)
	}

	// Check content contains expected elements
	if !contains(content, "FROM node:20-alpine") {
		t.Error("Expected base image 'node:20-alpine'")
	}
	if !contains(content, "npm ci") {
		t.Error("Expected 'npm ci' command")
	}
	if !contains(content, "RUN npm run build") {
		t.Error("Expected build command")
	}
	if !contains(content, "ENV PORT=3000") {
		t.Error("Expected PORT environment variable")
	}
	if !contains(content, "EXPOSE 3000") {
		t.Error("Expected EXPOSE 3000")
	}
	if !contains(content, "ENV NODE_ENV=production") {
		t.Error("Expected NODE_ENV environment variable")
	}
	if !contains(content, "CMD [\"sh\", \"-c\", \"npm start\"]") {
		t.Error("Expected run command")
	}

	t.Logf("✓ Generated correct Node.js Dockerfile")
}

func TestGenerateDockerfile_Go(t *testing.T) {
	t.Logf("Testing Go Dockerfile generation")

	gen := NewGenerator(3000, 3100)
	tempDir := t.TempDir()

	content, err := gen.GenerateDockerfile("golang", "", 3001, nil, "go build -o app", "./app", tempDir)
	if err != nil {
		t.Fatalf("Failed to generate Dockerfile: %v", err)
	}

	// Check content contains expected elements
	if !contains(content, "FROM golang:1.23-alpine") {
		t.Error("Expected base image 'golang:1.23-alpine'")
	}
	if !contains(content, "RUN go build -o app") {
		t.Error("Expected build command")
	}
	if !contains(content, "ENV PORT=3001") {
		t.Error("Expected PORT environment variable")
	}
	if !contains(content, "CMD [\"sh\", \"-c\", \"./app\"]") {
		t.Error("Expected run command")
	}

	t.Logf("✓ Generated correct Go Dockerfile")

	// Verify multi-stage aspects
	if !contains(content, "AS builder") {
		t.Error("Expected multi-stage build with 'AS builder'")
	}
	if !contains(content, "FROM alpine:latest") {
		t.Error("Expected final stage using 'alpine:latest'")
	}
	if !contains(content, "COPY --from=builder") {
		t.Error("Expected COPY --from=builder in multi-stage build")
	}
	t.Logf("✓ Multi-stage build correctly configured for Go")
}

func TestGenerateDockerfile_Rust(t *testing.T) {
	t.Logf("Testing Rust Dockerfile generation with multi-stage")

	gen := NewGenerator(3000, 3100)
	tempDir := t.TempDir()

	content, err := gen.GenerateDockerfile("rust", "", 3002, nil, "cargo build --release", "./app", tempDir)
	if err != nil {
		t.Fatalf("Failed to generate Dockerfile: %v", err)
	}

	// Check content contains expected elements
	if !contains(content, "FROM rust:1.75-slim") {
		t.Error("Expected base image 'rust:1.75-slim'")
	}
	if !contains(content, "RUN cargo build --release") {
		t.Error("Expected build command")
	}
	if !contains(content, "ENV PORT=3002") {
		t.Error("Expected PORT environment variable")
	}
	if !contains(content, "CMD [\"sh\", \"-c\", \"./app\"]") {
		t.Error("Expected run command")
	}

	// Verify multi-stage aspects
	if !contains(content, "AS builder") {
		t.Error("Expected multi-stage build with 'AS builder'")
	}
	if !contains(content, "FROM alpine:latest") {
		t.Error("Expected final stage using 'alpine:latest'")
	}
	if !contains(content, "COPY --from=builder") {
		t.Error("Expected COPY --from=builder in multi-stage build")
	}
	if !contains(content, "COPY --from=builder /app /app") {
		t.Error("Expected copy of build output into runtime image")
	}

	t.Logf("✓ Multi-stage build correctly configured for Rust")
}

func TestGenerateDockerfile_BaseImageOverride(t *testing.T) {
	t.Logf("Testing base image override")

	gen := NewGenerator(3000, 3100)
	tempDir := t.TempDir()

	customImage := "node:18-slim"
	content, err := gen.GenerateDockerfile("nodejs", customImage, 3000, nil, "npm run build", "npm start", tempDir)
	if err != nil {
		t.Fatalf("Failed to generate Dockerfile: %v", err)
	}

	if !contains(content, "FROM node:18-slim") {
		t.Errorf("Expected custom base image 'node:18-slim', got:\n%s", content)
	}

	t.Logf("✓ Correctly used custom base image")
}

func TestCheckDockerfileExists(t *testing.T) {
	t.Logf("Testing Dockerfile detection")

	gen := NewGenerator(3000, 3100)
	tempDir := t.TempDir()

	// No Dockerfile exists
	path, exists := gen.CheckDockerfileExists(tempDir)
	if exists {
		t.Error("Should not find Dockerfile in empty directory")
	}

	// Create Dockerfile
	dockerfile := `FROM alpine
CMD ["echo", "hello"]`
	if err := os.WriteFile(filepath.Join(tempDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		t.Fatalf("Failed to create Dockerfile: %v", err)
	}

	path, exists = gen.CheckDockerfileExists(tempDir)
	if !exists {
		t.Error("Should find Dockerfile")
	}
	if path == "" {
		t.Error("Should return path to Dockerfile")
	}

	t.Logf("✓ Correctly detected existing Dockerfile")
}

func TestWriteDockerfile(t *testing.T) {
	t.Logf("Testing Dockerfile writing")

	gen := NewGenerator(3000, 3100)
	tempDir := t.TempDir()

	content := `FROM node:20-alpine
WORKDIR /app
COPY . .
CMD ["npm", "start"]`

	path, err := gen.WriteDockerfile(content, tempDir)
	if err != nil {
		t.Fatalf("Failed to write Dockerfile: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("Dockerfile.auto was not created")
	}

	// Verify content
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read Dockerfile: %v", err)
	}

	if string(written) != content {
		t.Error("Written content does not match")
	}

	t.Logf("✓ Successfully wrote Dockerfile")
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
	if start+len(substr) > len(s) {
		return false
	}
	if s[start:start+len(substr)] == substr {
		return true
	}
	return containsAt(s, substr, start+1)
}
