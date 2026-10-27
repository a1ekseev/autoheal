package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContainerName(t *testing.T) {
	assert.Equal(t, "web", containerName("id", []string{"/web"}))
	// Legacy links add "/<linker>/<alias>" entries; the real name has no inner slash.
	assert.Equal(t, "db", containerName("id", []string{"/app/db", "/db"}))
	assert.Equal(t, "id", containerName("id", nil))
}
