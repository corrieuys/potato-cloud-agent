package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const MaxImageHashes = 5

// ImageManager handles image deduplication and retention.
type ImageManager struct {
	dataDir string
	verbose bool
}

// DockerImage represents a Docker image with metadata.
type DockerImage struct {
	ID        string
	Tag       string
	Hash      string
	CreatedAt time.Time
}

// NewImageManager creates an image manager.
func NewImageManager(dataDir string, _ *Manager) *ImageManager {
	return &ImageManager{dataDir: dataDir}
}

// GetLastNImageHashes returns the most recent unique image hashes for a tag prefix.
func (m *ImageManager) GetLastNImageHashes(imageTagPrefix string) ([]string, error) {
	images, err := m.listImages(imageTagPrefix)
	if err != nil {
		return nil, err
	}

	hashes := make([]string, 0, MaxImageHashes)
	seen := make(map[string]struct{})
	for _, img := range images {
		if _, exists := seen[img.Hash]; exists {
			continue
		}
		seen[img.Hash] = struct{}{}
		hashes = append(hashes, img.Hash)
		if len(hashes) == MaxImageHashes {
			break
		}
	}
	return hashes, nil
}

func (m *ImageManager) cleanupOldImages(imageTagPrefix string, keep int) error {
	images, err := m.listImages(imageTagPrefix)
	if err != nil {
		return err
	}
	if len(images) <= keep {
		return nil
	}

	for _, img := range images[keep:] {
		m.logVerbose("Removing old image: %s (%s)", img.Tag, img.ID)
		if err := m.removeImage(img.ID); err != nil {
			m.logVerbose("Failed to remove image %s: %v", img.ID, err)
		}
	}
	return nil
}

func (m *ImageManager) listImages(imageTagPrefix string) ([]DockerImage, error) {
	output, err := exec.Command(
		"docker", "images",
		"--format", "{{.Repository}}:{{.Tag}}|{{.ID}}|{{.CreatedAt}}",
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker images failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	images := make([]DockerImage, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}

		tag := parts[0]
		if imageTagPrefix != "" && !strings.HasPrefix(tag, imageTagPrefix) {
			continue
		}

		created, err := time.Parse("2006-01-02 15:04:05 -0700 MST", parts[2])
		if err != nil {
			created = time.Time{}
		}

		hash := m.getImageHash(parts[1], tag)
		images = append(images, DockerImage{
			ID:        parts[1],
			Tag:       tag,
			Hash:      hash,
			CreatedAt: created,
		})
	}

	sort.Slice(images, func(i, j int) bool {
		return images[i].CreatedAt.After(images[j].CreatedAt)
	})

	return images, nil
}

func (m *ImageManager) getImageHash(imageID, imageTag string) string {
	base := imageID
	if strings.TrimSpace(base) == "" {
		base = imageTag
	}
	sum := sha256.Sum256([]byte(base))
	return hex.EncodeToString(sum[:])
}

func (m *ImageManager) removeImage(imageID string) error {
	output, err := exec.Command("docker", "rmi", "-f", imageID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rmi failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (m *ImageManager) logVerbose(format string, args ...interface{}) {
	if m.verbose {
		fmt.Printf("[IMAGE] "+format+"\n", args...)
	}
}
