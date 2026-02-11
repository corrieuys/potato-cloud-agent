package service

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	containerpkg "github.com/buildvigil/agent/internal/container"
)

const imageRetentionCountDefault = 5

var (
	buildImage         = defaultBuildImage
	runContainer       = defaultRunContainer
	stopContainer      = defaultStopContainer
	renameContainer    = defaultRenameContainer
	containerExists    = defaultContainerExists
	getContainerStatus = defaultGetContainerStatus
	listImages         = defaultListImages
	removeImage        = defaultRemoveImage

	stackNetworkOnce sync.Once
	stackNetworkMgr  *containerpkg.StackNetworkManager
	stackNetworkErr  error
)

func initStackNetworkManager() error {
	stackNetworkOnce.Do(func() {
		stackNetworkMgr, stackNetworkErr = containerpkg.NewStackNetworkManager()
	})
	return stackNetworkErr
}

func defaultBuildImage(repoPath, dockerfilePath, imageTag string) error {
	args := []string{"build", "-f", dockerfilePath, "-t", imageTag, repoPath}
	cmd := exec.Command("docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker build failed: %w\nOutput: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func defaultRunContainer(imageTag, containerName string, port int, envVars, secrets map[string]string) (string, error) {
	_ = stopContainer(containerName)

	args := []string{"run", "-d", "--name", containerName}

	for key, value := range envVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}
	for key, value := range secrets {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	if port > 0 {
		args = append(args, "-p", fmt.Sprintf("%d:%d", port, port))
	}

	args = append(args, imageTag)

	cmd := exec.Command("docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker run failed: %w\nOutput: %s", err, strings.TrimSpace(string(output)))
	}

	return strings.TrimSpace(string(output)), nil
}

func defaultStopContainer(containerName string) error {
	if strings.TrimSpace(containerName) == "" {
		return nil
	}

	_, _ = exec.Command("docker", "stop", "-t", "10", containerName).CombinedOutput()

	output, err := exec.Command("docker", "rm", "-f", containerName).CombinedOutput()
	if err != nil {
		msg := string(output)
		if strings.Contains(msg, "No such container") || strings.Contains(msg, "No such object") {
			return nil
		}
		return fmt.Errorf("docker rm failed: %w\nOutput: %s", err, strings.TrimSpace(msg))
	}
	return nil
}

func defaultRenameContainer(oldName, newName string) error {
	_ = stopContainer(newName)

	output, err := exec.Command("docker", "rename", oldName, newName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rename failed: %w\nOutput: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func defaultContainerExists(containerName string) bool {
	return exec.Command("docker", "inspect", "--format", "{{.Id}}", containerName).Run() == nil
}

func defaultGetContainerStatus(containerName string) (string, error) {
	if strings.TrimSpace(containerName) == "" {
		return "stopped", nil
	}

	output, err := exec.Command("docker", "inspect", "--format", "{{.State.Status}}", containerName).CombinedOutput()
	if err != nil {
		msg := string(output)
		if strings.Contains(msg, "No such object") || strings.Contains(msg, "No such container") {
			return "stopped", nil
		}
		return "error", fmt.Errorf("docker inspect failed: %w\nOutput: %s", err, strings.TrimSpace(msg))
	}

	status := strings.TrimSpace(string(output))
	if status == "" {
		status = "unknown"
	}
	return status, nil
}

func defaultListImages(serviceID string) ([]ImageInfo, error) {
	linesA, errA := dockerImagesByReference(fmt.Sprintf("%s/%s:*", ImagePrefix, serviceID))
	linesB, errB := dockerImagesByReference(fmt.Sprintf("%s-%s:*", ImagePrefix, serviceID))
	if errA != nil && errB != nil {
		return nil, fmt.Errorf("docker images failed: %v; %v", errA, errB)
	}

	seen := make(map[string]struct{})
	images := make([]ImageInfo, 0, len(linesA)+len(linesB))
	for _, line := range append(linesA, linesB...) {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		if _, exists := seen[parts[1]]; exists {
			continue
		}
		seen[parts[1]] = struct{}{}
		images = append(images, ImageInfo{
			Tag:       parts[0],
			ID:        parts[1],
			CreatedAt: parts[2],
		})
	}

	return images, nil
}

func dockerImagesByReference(reference string) ([]string, error) {
	output, err := exec.Command(
		"docker", "images",
		"--filter", "reference="+reference,
		"--format", "{{.Repository}}:{{.Tag}}|{{.ID}}|{{.CreatedAt}}",
	).CombinedOutput()
	if err != nil {
		return nil, err
	}

	text := strings.TrimSpace(string(output))
	if text == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

func defaultRemoveImage(imageID string) error {
	output, err := exec.Command("docker", "rmi", "-f", imageID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rmi failed: %w\nOutput: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// ConnectContainerToStackNetwork connects a container to its stack's network.
func ConnectContainerToStackNetwork(containerID, stackID string) error {
	if err := initStackNetworkManager(); err != nil {
		return fmt.Errorf("stack network manager not initialized: %w", err)
	}
	if err := stackNetworkMgr.CreateStackNetwork(stackID); err != nil {
		return err
	}
	return stackNetworkMgr.ConnectContainerToStackNetwork(stackID, containerID)
}

// DisconnectContainerFromStackNetwork disconnects a container from its stack's network.
func DisconnectContainerFromStackNetwork(containerID, stackID string) error {
	if err := initStackNetworkManager(); err != nil {
		return fmt.Errorf("stack network manager not initialized: %w", err)
	}
	return stackNetworkMgr.DisconnectContainerFromStackNetwork(stackID, containerID)
}

// DeleteStackNetwork deletes a stack's network.
func DeleteStackNetwork(stackID string) error {
	if err := initStackNetworkManager(); err != nil {
		return fmt.Errorf("stack network manager not initialized: %w", err)
	}
	return stackNetworkMgr.DeleteStackNetwork(stackID)
}

// ListStackNetworks returns all stack networks.
func ListStackNetworks() ([]containerpkg.NetworkResource, error) {
	if err := initStackNetworkManager(); err != nil {
		return nil, fmt.Errorf("stack network manager not initialized: %w", err)
	}
	return stackNetworkMgr.ListStackNetworks()
}

// GetStackNetworkName returns the network name for a stack.
func GetStackNetworkName(stackID string) string {
	return fmt.Sprintf("stack-%s-network", stackID)
}

// IsStackNetworkCreated checks if a stack network exists.
func IsStackNetworkCreated(stackID string) bool {
	networks, err := ListStackNetworks()
	if err != nil {
		return false
	}
	target := GetStackNetworkName(stackID)
	for _, network := range networks {
		if network.Name == target {
			return true
		}
	}
	return false
}

// cleanupOldImages removes old Docker images for a service.
func (m *Manager) cleanupOldImages(serviceID string) {
	images, err := listImages(serviceID)
	if err != nil {
		m.logVerbose("Failed to list images for cleanup: %v", err)
		return
	}
	if len(images) <= imageRetentionCountDefault {
		return
	}

	sort.Slice(images, func(i, j int) bool {
		iTime, iErr := time.Parse("2006-01-02 15:04:05 -0700 MST", images[i].CreatedAt)
		jTime, jErr := time.Parse("2006-01-02 15:04:05 -0700 MST", images[j].CreatedAt)
		if iErr != nil || jErr != nil {
			return images[i].CreatedAt > images[j].CreatedAt
		}
		return iTime.After(jTime)
	})

	for _, img := range images[imageRetentionCountDefault:] {
		m.logVerbose("Removing old image: %s", img.Tag)
		if err := removeImage(img.ID); err != nil {
			m.logVerbose("Failed to remove image %s: %v", img.Tag, err)
		}
	}
}
