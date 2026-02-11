package container

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
)

const ImageRetentionCount = 10

type Generator struct {
	portRangeStart int
	portRangeEnd   int
	usedPorts      map[int]bool
	mu             sync.Mutex
}

func NewGenerator(startPort, endPort int) *Generator {
	return &Generator{
		portRangeStart: startPort,
		portRangeEnd:   endPort,
		usedPorts:      make(map[int]bool),
	}
}

// LanguageConfig holds configuration for each supported language
type LanguageConfig struct {
	DefaultBaseImage string
	Template         string
	DetectFiles      []string
}

var LanguageConfigs = map[string]LanguageConfig{
	"nodejs": {
		DefaultBaseImage: "node:20-alpine",
		Template:         nodejsDockerfile,
		DetectFiles:      []string{"package.json", "package-lock.json"},
	},
	"golang": {
		DefaultBaseImage: "golang:1.23-alpine",
		Template:         golangDockerfile,
		DetectFiles:      []string{"go.mod", "go.sum"},
	},
	"python": {
		DefaultBaseImage: "python:3.11-slim",
		Template:         pythonDockerfile,
		DetectFiles:      []string{"requirements.txt", "pyproject.toml"},
	},
	"rust": {
		DefaultBaseImage: "rust:1.75-slim",
		Template:         rustDockerfile,
		DetectFiles:      []string{"Cargo.toml"},
	},
	"java": {
		DefaultBaseImage: "eclipse-temurin:21-jre-alpine",
		Template:         javaDockerfile,
		DetectFiles:      []string{"pom.xml", "build.gradle"},
	},
	"generic": {
		DefaultBaseImage: "alpine:latest",
		Template:         genericDockerfile,
		DetectFiles:      []string{},
	},
}

// TemplateData holds the data for Dockerfile template
type TemplateData struct {
	BaseImage    string
	Port         int
	EnvVars      map[string]string
	BuildCommand string
	RunCommand   string
}

// DetectLanguage automatically detects the language/runtime from repository files
func (g *Generator) DetectLanguage(repoPath string) string {
	for lang, config := range LanguageConfigs {
		for _, file := range config.DetectFiles {
			if _, err := os.Stat(filepath.Join(repoPath, file)); err == nil {
				return lang
			}
		}
	}
	return "generic"
}

// GenerateDockerfile creates a Dockerfile for the given service
func (g *Generator) GenerateDockerfile(language, baseImage string, port int, envVars map[string]string, buildCommand, runCommand, repoPath string) (string, error) {
	if language == "" || language == "auto" {
		language = g.DetectLanguage(repoPath)
	}
	if strings.TrimSpace(buildCommand) == "" || strings.TrimSpace(runCommand) == "" {
		return "", fmt.Errorf("build_command and run_command are required for generated Dockerfiles")
	}

	config, ok := LanguageConfigs[language]
	if !ok {
		config = LanguageConfigs["generic"]
	}

	// Use user-provided base image or default
	if baseImage == "" {
		baseImage = config.DefaultBaseImage
	}

	data := TemplateData{
		BaseImage:    baseImage,
		Port:         port,
		EnvVars:      envVars,
		BuildCommand: buildCommand,
		RunCommand:   runCommand,
	}

	tmpl, err := template.New("dockerfile").Parse(config.Template)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// WriteDockerfile writes the generated Dockerfile to disk
func (g *Generator) WriteDockerfile(content, repoPath string) (string, error) {
	dockerfilePath := filepath.Join(repoPath, "Dockerfile.auto")
	if err := os.WriteFile(dockerfilePath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write Dockerfile: %w", err)
	}
	return dockerfilePath, nil
}

// CheckDockerfileExists checks if a Dockerfile already exists in the repository
func (g *Generator) CheckDockerfileExists(repoPath string) (string, bool) {
	paths := []string{
		filepath.Join(repoPath, "Dockerfile"),
		filepath.Join(repoPath, "dockerfile"),
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}

	return "", false
}
