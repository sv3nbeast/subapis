package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

func grokPreviousResponseSessionSeed(body []byte) string {
	id := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
	if id == "" || ClassifyOpenAIPreviousResponseIDKind(id) != OpenAIPreviousResponseIDKindResponseID {
		return ""
	}
	return "grok-prev-resp:" + id
}
