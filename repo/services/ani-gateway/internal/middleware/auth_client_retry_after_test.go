package middleware

import (
	"context"
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWriteAuthRPCErrorSetsRetryAfterHeader(t *testing.T) {
	grpcErr, err := status.New(codes.ResourceExhausted, "rate limited").WithDetails(&errdetails.ErrorInfo{
		Reason:   "RATE_LIMITED",
		Metadata: map[string]string{"retry_after_seconds": "23"},
	})
	if err != nil {
		t.Fatalf("WithDetails: %v", err)
	}
	h := server.New()
	h.GET("/limited", func(ctx context.Context, c *app.RequestContext) {
		writeAuthRPCError(c, grpcErr.Err())
	})
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/limited", nil).Result()
	if got := resp.Header.Get("Retry-After"); got != "23" {
		t.Fatalf("Retry-After = %q, want 23", got)
	}
}
