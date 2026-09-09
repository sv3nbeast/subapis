package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWebChatOptionsFailsClosedWhenTaskServiceIsNotConfigured(t *testing.T) {
	svc := NewWebChatService(&webChatRepoStub{}, nil, agentKeyStub{}, agentCatalogStub{}, webChatRuntimeStub{enabled: true})
	options, err := svc.Options(context.Background(), 1)
	require.NoError(t, err)
	require.False(t, options.TasksEnabled)
	require.Equal(t, "not_configured", options.TaskStatus)
	require.NotNil(t, options.TaskLimits)
}
