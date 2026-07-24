package sourcediscovery

import (
	"context"
	"errors"
	"time"
)

type nextProcessor interface {
	ProcessNext(context.Context) (bool, error)
}

type Service struct {
	processor    nextProcessor
	pollInterval time.Duration
}

func NewService(processor nextProcessor, pollInterval time.Duration) *Service {
	return &Service{processor: processor, pollInterval: pollInterval}
}

func (s *Service) Run(ctx context.Context) error {
	if s == nil || s.processor == nil || s.pollInterval <= 0 {
		return errors.New("invalid Source Discovery Service")
	}
	for ctx.Err() == nil {
		processed, err := s.processor.ProcessNext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if processed {
			continue
		}
		timer := time.NewTimer(s.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}
