package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type groupPricingHandlerService struct {
	service.AdminService
	created *service.CreateGroupInput
	updated *service.UpdateGroupInput
}

func (s *groupPricingHandlerService) CreateGroup(_ context.Context, input *service.CreateGroupInput) (*service.Group, error) {
	s.created = input
	return &service.Group{ID: 9, Platform: input.Platform, ModelPricing: input.ModelPricing}, nil
}

func (s *groupPricingHandlerService) UpdateGroup(_ context.Context, _ int64, input *service.UpdateGroupInput) (*service.Group, error) {
	s.updated = input
	return &service.Group{ID: 9, Platform: "zhipu", ModelPricing: *input.ModelPricing}, nil
}

func TestGroupPricingHandlerPreservesSnakeCaseWireContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, update := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "update"}[update], func(t *testing.T) {
			svc := &groupPricingHandlerService{}
			h := NewGroupHandler(svc, nil, nil)
			body := `{"name":"GLM","platform":"zhipu","rate_multiplier":1,"long_context_pricing_enabled":true,"model_pricing":[{"models":["glm-test"],"billing_mode":"token","input_price":0.000002,"output_price":0.000006,"cache_read_price":0.0000002,"enabled":false}]}`
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Params = []gin.Param{{Key: "id", Value: "9"}}
			if update {
				h.Update(c)
			} else {
				h.Create(c)
			}
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var pricing []service.ChannelModelPricing
			if update {
				pricing = *svc.updated.ModelPricing
				require.True(t, *svc.updated.LongContextPricingEnabled)
			} else {
				pricing = svc.created.ModelPricing
				require.True(t, svc.created.LongContextPricingEnabled)
			}
			require.Len(t, pricing, 1)
			require.Equal(t, "zhipu", pricing[0].Platform)
			require.Equal(t, 0.000002, *pricing[0].InputPrice)
			require.True(t, pricing[0].Disabled)
			wire := gjson.Get(rec.Body.String(), "data.model_pricing.0")
			require.Equal(t, 0.000002, wire.Get("input_price").Float())
			require.False(t, wire.Get("enabled").Bool())
			require.False(t, wire.Get("InputPrice").Exists())
		})
	}
}
