package router

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestGatewayDoesNotRegisterChatCompletionsProxy(t *testing.T) {
	h := server.New()
	RegisterWithOptions(h, RegisterOptions{})

	payload := `{"model":"unused","messages":[]}`
	resp := ut.PerformRequest(
		h.Engine,
		http.MethodPost,
		"/v1/chat/completions",
		&ut.Body{Body: strings.NewReader(payload), Len: len(payload)},
	).Result()
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("POST /v1/chat/completions status = %d, want 404; body=%s", resp.StatusCode(), resp.Body())
	}
}
