// Package gatewaypublish renders and applies the narrowly scoped Envoy AI
// Gateway objects owned by the inference-service publisher.
package gatewaypublish

import (
	"context"
	"errors"
)

type Kind string

const (
	KindBackend          Kind = "Backend"
	KindAIServiceBackend Kind = "AIServiceBackend"
	KindAIGatewayRoute   Kind = "AIGatewayRoute"
	KindGateway          Kind = "Gateway"
)

var (
	ErrNotFound       = errors.New("kubernetes object not found")
	ErrStaleCondition = errors.New("kubernetes object has a stale condition")
)

type Object struct {
	Kind       Kind
	Namespace  string
	Name       string
	Generation int64
	Body       map[string]any
	Status     map[string]any
}

type Condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	ObservedGeneration int64  `json:"observedGeneration"`
}

type RouteParentStatus struct {
	ParentRef      map[string]any `json:"parentRef"`
	ControllerName string         `json:"controllerName"`
	Conditions     []Condition    `json:"conditions"`
}

type Objects struct {
	Backend          Object
	AIServiceBackend Object
	AIGatewayRoute   Object
}

type KubeAPI interface {
	Apply(context.Context, Object) (Object, error)
	Get(context.Context, Kind, string, string) (Object, error)
	Delete(context.Context, Object) error
}
