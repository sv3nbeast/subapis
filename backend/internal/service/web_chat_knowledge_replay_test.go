package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type knowledgeReplayChat struct {
	*webChatRepoStub
	turns int
}

func (r *knowledgeReplayChat) RegenerateTurn(context.Context, int64, int64, int64, ...WebChatTarget) (*WebChatMessage, error) {
	r.turns++
	return &WebChatMessage{ID: 101, Role: "assistant"}, nil
}
func (r *knowledgeReplayChat) ReviseTurn(context.Context, int64, int64, int64, string, string, ...WebChatTarget) (*WebChatMessage, *WebChatMessage, error) {
	r.turns++
	return &WebChatMessage{ID: 100, Role: "user"}, &WebChatMessage{ID: 101, Role: "assistant"}, nil
}

type knowledgeReplayDocs struct {
	*webChatDocumentRepoTestDouble
	ids      []int64
	err      error
	searches int
}

func (r *knowledgeReplayDocs) MessageDocumentIDs(context.Context, int64, int64) ([]int64, error) {
	return r.ids, r.err
}
func (r *knowledgeReplayDocs) SearchDocumentChunks(_ context.Context, _ int64, project int64, _ []int64, _ string, _ int) ([]WebChatDocumentChunk, error) {
	r.searchedProjectID = project
	r.searches++
	return r.searchChunks, nil
}

func TestWebChatReplayKeepsExplicitFilesWithoutReenablingProjectSearch(t *testing.T) {
	for _, revise := range []bool{false, true} {
		project := int64(77)
		repo := &knowledgeReplayChat{webChatRepoStub: &webChatRepoStub{session: &WebChatSession{ID: 2, UserID: 1, GroupID: 7, Model: "test-model", ProjectID: &project, KnowledgeEnabled: false}, recent: []WebChatMessage{{Role: "user", Content: "revised unrelated question"}}}}
		docs := &knowledgeReplayDocs{webChatDocumentRepoTestDouble: &webChatDocumentRepoTestDouble{searchChunks: []WebChatDocumentChunk{{ID: 1, DocumentID: 10, DocumentName: "explicit.csv", Content: "selected file data"}}}, ids: []int64{10}}
		settings := newWebChatDocumentSettingsTestDouble()
		settings.values[SettingKeyWebChatFilesEnabled] = "true"
		chat := NewWebChatService(repo, agentPlannerKeyRepo{}, &agentPlannerKeys{}, agentCatalogStub{}, webChatRuntimeStub{enabled: true, history: true})
		chat.SetDocumentService(NewWebChatDocumentService(docs, settings, nil, nil))
		var result *WebChatGeneration
		var err error
		if revise {
			result, err = chat.PrepareRevise(context.Background(), 1, 2, 5, "new question")
		} else {
			result, err = chat.PrepareRegenerate(context.Background(), 1, 2, 5)
		}
		require.NoError(t, err)
		require.Equal(t, 1, docs.searches)
		require.Zero(t, docs.searchedProjectID)
		require.Len(t, result.Sources, 1)
		require.Equal(t, "explicit", result.Sources[0].Origin)
	}
}
func TestWebChatReplayDoesNotDropAttachmentsWhenLookupFails(t *testing.T) {
	repo := &knowledgeReplayChat{webChatRepoStub: &webChatRepoStub{session: &WebChatSession{ID: 2, UserID: 1, GroupID: 7, Model: "test-model"}}}
	docs := &knowledgeReplayDocs{webChatDocumentRepoTestDouble: &webChatDocumentRepoTestDouble{}, err: errors.New("attachment lookup failed")}
	chat := NewWebChatService(repo, agentPlannerKeyRepo{}, &agentPlannerKeys{}, agentCatalogStub{}, webChatRuntimeStub{enabled: true, history: true})
	chat.SetDocumentService(NewWebChatDocumentService(docs, nil, nil, nil))
	_, err := chat.PrepareRegenerate(context.Background(), 1, 2, 5)
	require.ErrorContains(t, err, "attachment lookup failed")
	require.Zero(t, repo.turns)
}
