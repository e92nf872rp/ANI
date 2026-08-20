package runtime

import (
	"fmt"

	"github.com/google/uuid"
)

// MutationIdempotencyKey 是一次 generation 打 Core 的稳定幂等键。
// 请求路径 dispatch 和 worker 必须复用同一个 key，避免重复 POST platform-workloads。
func MutationIdempotencyKey(serviceID uuid.UUID, generation int64) uuid.UUID {
	name := fmt.Sprintf("ani/inference-runtime/%s/generation/%d", serviceID, generation)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(name))
}
