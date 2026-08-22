package sourceadmission

import (
	"context"
	"errors"

	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcejobs"
)

type Qualification struct {
	StoredAssessment
	PauseBeforeProjection bool
}

type Service struct {
	store    *Store
	verifier *Verifier
	mode     Mode
}

func NewService(store *Store, verifier *Verifier, mode Mode) (*Service, error) {
	if store == nil || store.pool == nil || verifier == nil || (mode != ModeShadow && mode != ModeEnforcement) {
		return nil, errors.New("invalid Source Admission Service")
	}
	return &Service{store: store, verifier: verifier, mode: mode}, nil
}

func NewShadowServiceWithoutSearch(store *Store) *Service {
	verifier, err := NewVerifier(nil, DefaultVerifierConfig("not-configured"))
	if err != nil {
		panic(err)
	}
	service, err := NewService(store, verifier, ModeShadow)
	if err != nil {
		panic(err)
	}
	return service
}

func (service *Service) Qualify(
	ctx context.Context,
	lease sourcejobs.Lease,
	item source.Source,
	revisionID string,
	artifact normalize.Artifact,
) (Qualification, error) {
	if service == nil || service.store == nil || service.verifier == nil {
		return Qualification{}, errors.New("invalid Source Admission Service")
	}
	policy := service.verifier.config.Policy
	policyHash, err := PolicySHA256(policy)
	if err != nil {
		return Qualification{}, err
	}
	stored, ok, err := service.store.Current(ctx, item.ID, revisionID, policyHash)
	if err != nil {
		return Qualification{}, err
	}
	if ok {
		if err := validateAssessment(policy, stored.Assessment); err != nil {
			return Qualification{}, err
		}
		return qualificationFromStored(stored), nil
	}
	assessment, err := service.verifier.Verify(ctx, Profile{
		InputKind: item.InputKind, Title: item.Title, OriginalURL: item.OriginURL, FinalURL: item.FinalURL,
		ContentSHA256: item.ContentSHA256, ArtifactSHA256: artifact.SHA256,
	}, artifact)
	if err != nil {
		return Qualification{}, err
	}
	stored, _, err = service.store.Publish(ctx, PublishCommand{
		Lease: lease, RevisionID: revisionID, Mode: service.mode, Policy: policy, Assessment: assessment,
	})
	if err != nil {
		return Qualification{}, err
	}
	return qualificationFromStored(stored), nil
}

func qualificationFromStored(stored StoredAssessment) Qualification {
	return Qualification{
		StoredAssessment:      stored,
		PauseBeforeProjection: stored.Mode == ModeEnforcement && stored.Report.Status == StatusReviewRequired,
	}
}
