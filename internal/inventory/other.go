//go:build !linux && !windows

package inventory

import (
	"context"
	"errors"
)

type OSInspector struct{}

func (OSInspector) List(context.Context) ([]Volume, error) { return nil, errors.New("UNSUPPORTED_OS") }
