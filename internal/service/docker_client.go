package service

// DockerClient defines the interface for Docker operations
type DockerClient interface {
	// BuildImage builds a Docker image from a Dockerfile
	BuildImage(repoPath, dockerfilePath, imageTag string) error

	// RunContainer starts a new container and returns the container ID
	RunContainer(imageTag, containerName string, port int, envVars, secrets map[string]string) (string, error)

	// StopContainer stops and removes a container
	StopContainer(containerName string) error

	// RenameContainer renames a container
	RenameContainer(oldName, newName string) error

	// GetContainerStatus returns the status of a container (running, stopped, etc.)
	GetContainerStatus(containerName string) (string, error)

	// ContainerExists checks if a container exists
	ContainerExists(containerName string) bool

	// ListImages lists all images for a service
	ListImages(serviceID string) ([]ImageInfo, error)

	// RemoveImage removes a Docker image by ID
	RemoveImage(imageID string) error
}

// ImageInfo represents information about a Docker image
type ImageInfo struct {
	Tag       string
	ID        string
	CreatedAt string
}

// RealDockerClient implements DockerClient using actual Docker commands
type RealDockerClient struct{}

// NewRealDockerClient creates a new real Docker client
func NewRealDockerClient() DockerClient {
	return &RealDockerClient{}
}

func (r *RealDockerClient) BuildImage(repoPath, dockerfilePath, imageTag string) error {
	return buildImage(repoPath, dockerfilePath, imageTag)
}

func (r *RealDockerClient) RunContainer(imageTag, containerName string, port int, envVars, secrets map[string]string) (string, error) {
	return runContainer(imageTag, containerName, port, envVars, secrets)
}

func (r *RealDockerClient) StopContainer(containerName string) error {
	return stopContainer(containerName)
}

func (r *RealDockerClient) RenameContainer(oldName, newName string) error {
	return renameContainer(oldName, newName)
}

func (r *RealDockerClient) GetContainerStatus(containerName string) (string, error) {
	return getContainerStatus(containerName)
}

func (r *RealDockerClient) ContainerExists(containerName string) bool {
	return containerExists(containerName)
}

func (r *RealDockerClient) ListImages(serviceID string) ([]ImageInfo, error) {
	return listImages(serviceID)
}

func (r *RealDockerClient) RemoveImage(imageID string) error {
	return removeImage(imageID)
}
