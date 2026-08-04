package nats

import (
	natsgo "github.com/nats-io/nats.go"
)

// jetStream 是 natsgo.JetStreamContext 的内部接口包装，仅用于 adapter 和单测 mock。
// natsgo.JetStreamContext 是具体接口类型（legacy API），这里抽取 adapter 实际使用的 5 个方法，
// 使单测可用 fake 实现替换，生产路径仍直接持有 natsgo.JetStreamContext，零开销。
//
// 该接口仅存在于 pkg/adapters/nats/ 内部，不暴露到 pkg/ports/。
// natsgo.JetStreamContext 天然满足此接口（隐式实现）。
type jetStream interface {
	PublishMsg(msg *natsgo.Msg, opts ...natsgo.PubOpt) (*natsgo.PubAck, error)
	Subscribe(subj string, cb natsgo.MsgHandler, opts ...natsgo.SubOpt) (*natsgo.Subscription, error)
	QueueSubscribe(subj, queue string, cb natsgo.MsgHandler, opts ...natsgo.SubOpt) (*natsgo.Subscription, error)
	StreamInfo(stream string, opts ...natsgo.JSOpt) (*natsgo.StreamInfo, error)
	AddStream(cfg *natsgo.StreamConfig, opts ...natsgo.JSOpt) (*natsgo.StreamInfo, error)
}

// 编译期断言：natsgo.JetStreamContext 天然满足 jetStream 接口，生产路径零开销。
// 若 nats.go 升级导致方法签名漂移，此处会在编译期立即报错。
var _ jetStream = (natsgo.JetStreamContext)(nil)
