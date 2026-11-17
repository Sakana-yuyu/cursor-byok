package client

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type startupCleanup struct {
	name string
	run  func(context.Context) error
}

type startupRollback []startupCleanup

func (r *startupRollback) add(name string, cleanup func(context.Context) error) {
	*r = append(*r, startupCleanup{name: name, run: cleanup})
}

func (r *startupRollback) run() error {
	var errs []error
	// Each resource gets its own deadline; an exhausted stop must not prevent
	// restoration of the next resource or hide its recovery error.
	for i := len(*r) - 1; i >= 0; i-- {
		cleanup := (*r)[i]
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := cleanup.run(ctx)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("回滚 %s 失败: %w", cleanup.name, err))
		}
	}
	*r = nil
	return errors.Join(errs...)
}
