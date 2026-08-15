package service

import "errors"

var (
	ErrInvalidInput               = errors.New("invalid inference request")
	ErrUnsupportedTopology        = errors.New("unsupported inference topology")
	ErrAcceleratorSpecUnavailable = errors.New("accelerator spec is not available")
)
