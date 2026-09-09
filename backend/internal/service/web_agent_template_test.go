package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type templateOwnerRepo struct {
	*webChatRepoStub
	template *WebChatTemplate
}

func (r templateOwnerRepo) GetTemplate(context.Context, int64, int64, bool) (*WebChatTemplate, error) {
	return r.template, nil
}
func TestUsableTemplateEnforcesOwnerAndEnabledState(t *testing.T) {
	owner := int64(7)
	now := time.Now()
	other := int64(8)
	for _, tc := range []struct {
		name, scope string
		user        *int64
		enabled     bool
		want        error
	}{
		{"system", "system", nil, true, nil}, {"personal-owner", "personal", &owner, true, nil}, {"personal-other", "personal", &other, true, ErrWebChatTemplateNotFound}, {"disabled", "system", nil, false, ErrWebChatTemplateNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chat := NewWebChatService(templateOwnerRepo{webChatRepoStub: &webChatRepoStub{}, template: &WebChatTemplate{ID: 3, Scope: tc.scope, UserID: tc.user, Enabled: tc.enabled, Name: "Report", UpdatedAt: now}}, webChatAPIKeyRepoStub{}, agentKeyStub{}, agentCatalogStub{}, webChatRuntimeStub{enabled: true, templates: true})
			got, err := chat.usableTemplate(context.Background(), 7, 3)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, int64(3), got.ID)
			}
		})
	}
}
func TestWebAgentTemplateSnapshotIsMetadataOnly(t *testing.T) {
	s := WebAgentTemplateSnapshot{ID: 4, Name: "Data report", UpdatedAt: time.Now()}
	require.NotContains(t, s.Name, "{{")
}
