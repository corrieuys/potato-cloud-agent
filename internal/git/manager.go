package git

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Manager handles Git operations
type Manager struct {
	reposPath string
	keysDir   string
}

// NewManager creates a new Git manager
func NewManager(reposPath string, keysDir string) *Manager {
	return &Manager{reposPath: reposPath, keysDir: keysDir}
}

// getRepoPath returns the path for a service's repository
func (m *Manager) getRepoPath(serviceID string) string {
	return filepath.Join(m.reposPath, serviceID)
}

// CloneOrPull ensures a repository is cloned and at the desired ref/commit.
// If gitCommit is provided, it is checked out directly; otherwise gitRef is used.
// Returns the resolved commit hash that was checked out.
func (m *Manager) CloneOrPull(serviceID string, gitURL string, gitRef string, gitCommit string, sshKeyName string) (string, error) {
	repoPath := m.getRepoPath(serviceID)
	sshKeyName = strings.TrimSpace(sshKeyName)

	// Check if repo already exists
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		// Clone the repository
		if err := m.clone(gitURL, repoPath, sshKeyName); err != nil {
			return "", fmt.Errorf("failed to clone repository: %w", err)
		}
	}

	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return "", fmt.Errorf("failed to open repository: %w", err)
	}

	if gitCommit != "" {
		resolved, err := m.checkoutCommit(repo, gitCommit, sshKeyName)
		if err != nil {
			return "", err
		}
		return resolved, nil
	}

	if gitRef == "" {
		gitRef = "main"
	}

	resolved, err := m.checkoutRef(repo, gitRef, sshKeyName)
	if err != nil {
		return "", err
	}

	return resolved, nil
}

// clone clones a repository
func (m *Manager) clone(gitURL, destPath string, sshKeyName string) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	auth, err := m.authForRepo(gitURL, sshKeyName)
	if err != nil {
		return err
	}

	log.Printf("Git clone: url=%s sshKey=%q useSSH=%t auth=%t", gitURL, sshKeyName, isSSHGitURL(gitURL), auth != nil)

	_, err = git.PlainClone(destPath, false, &git.CloneOptions{
		URL:      gitURL,
		Progress: os.Stdout,
		Auth:     auth,
	})

	if err != nil {
		log.Printf("Git clone failed: %v", err)
		if auth != nil && strings.Contains(err.Error(), "invalid auth method") {
			log.Printf("Git clone retry without auth for url=%s", gitURL)
			_ = os.RemoveAll(destPath)
			_, retryErr := git.PlainClone(destPath, false, &git.CloneOptions{
				URL:      gitURL,
				Progress: os.Stdout,
			})
			if retryErr == nil {
				return nil
			}
			err = retryErr
			log.Printf("Git clone retry failed: %v", retryErr)
		}
		return fmt.Errorf("git clone failed: %w", err)
	}

	return nil
}

func (m *Manager) checkoutCommit(repo *git.Repository, commit string, sshKeyName string) (string, error) {
	worktree, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}

	auth, err := m.authForRepo(repoConfigURL(repo), sshKeyName)
	if err != nil {
		return "", err
	}

	// First fetch to ensure we have the commit
	if err := repo.Fetch(&git.FetchOptions{Progress: os.Stdout, Auth: auth}); err != nil && err != git.NoErrAlreadyUpToDate {
		_ = err
	}

	if err := worktree.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(commit), Force: true}); err != nil {
		return "", fmt.Errorf("failed to checkout commit %s: %w", commit, err)
	}

	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("failed to read HEAD: %w", err)
	}

	return head.Hash().String(), nil
}

func (m *Manager) checkoutRef(repo *git.Repository, ref string, sshKeyName string) (string, error) {
	worktree, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}

	auth, err := m.authForRepo(repoConfigURL(repo), sshKeyName)
	if err != nil {
		return "", err
	}

	if err := repo.Fetch(&git.FetchOptions{Progress: os.Stdout, Auth: auth}); err != nil && err != git.NoErrAlreadyUpToDate {
		_ = err
	}

	branchRef := plumbing.NewBranchReferenceName(ref)
	if err := worktree.Checkout(&git.CheckoutOptions{Branch: branchRef, Force: true}); err == nil {
		if err := worktree.Pull(&git.PullOptions{RemoteName: "origin", ReferenceName: branchRef, Force: true, Auth: auth}); err != nil && err != git.NoErrAlreadyUpToDate {
			_ = err
		}
		head, err := repo.Head()
		if err != nil {
			return "", fmt.Errorf("failed to read HEAD: %w", err)
		}
		return head.Hash().String(), nil
	}

	// Fallback to tag ref
	tagRef := plumbing.NewTagReferenceName(ref)
	refObj, err := repo.Reference(tagRef, true)
	if err != nil {
		return "", fmt.Errorf("failed to checkout ref %s: %w", ref, err)
	}

	if err := worktree.Checkout(&git.CheckoutOptions{Hash: refObj.Hash(), Force: true}); err != nil {
		return "", fmt.Errorf("failed to checkout tag %s: %w", ref, err)
	}

	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("failed to read HEAD: %w", err)
	}

	return head.Hash().String(), nil
}

func (m *Manager) authForRepo(gitURL, keyName string) (*gitssh.PublicKeys, error) {
	if keyName == "" {
		return nil, nil
	}
	if !isSSHGitURL(gitURL) {
		return nil, nil
	}

	keyPath := filepath.Join(m.keysDir, keyName)
	if _, err := os.Stat(keyPath); err != nil {
		return nil, nil
	}

	knownHostsPath := filepath.Join(m.keysDir, "known_hosts")
	if _, err := os.Stat(knownHostsPath); err != nil {
		home, herr := os.UserHomeDir()
		if herr == nil {
			fallback := filepath.Join(home, ".ssh", "known_hosts")
			if _, ferr := os.Stat(fallback); ferr == nil {
				knownHostsPath = fallback
			}
		}
	}

	callback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load known_hosts: %w", err)
	}

	keys, err := gitssh.NewPublicKeysFromFile("git", keyPath, "")
	if err != nil {
		return nil, fmt.Errorf("failed to load SSH key: %w", err)
	}
	keys.HostKeyCallback = callback

	return keys, nil
}

func isSSHGitURL(gitURL string) bool {
	trimmed := strings.TrimSpace(gitURL)
	return strings.HasPrefix(trimmed, "git@") ||
		strings.HasPrefix(trimmed, "ssh://") ||
		strings.HasPrefix(trimmed, "git+ssh://")
}

func repoConfigURL(repo *git.Repository) string {
	config, err := repo.Config()
	if err != nil {
		return ""
	}
	if origin, ok := config.Remotes["origin"]; ok {
		if len(origin.URLs) > 0 {
			return origin.URLs[0]
		}
	}
	return ""
}

// GetRepoPath returns the filesystem path for a service's repository
func (m *Manager) GetRepoPath(serviceID string) string {
	return m.getRepoPath(serviceID)
}

// RemoveRepo deletes a service's repository
func (m *Manager) RemoveRepo(serviceID string) error {
	repoPath := m.getRepoPath(serviceID)
	if err := os.RemoveAll(repoPath); err != nil {
		return fmt.Errorf("failed to remove repository: %w", err)
	}
	return nil
}
