// Package documentreading contains reusable document extraction primitives
// shared by durable Source processing and run-scoped Research URL evidence.
package documentreading

import (
	"context"
	"errors"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/documentrender"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
)

type VisionModels interface {
	DescribeImage(context.Context, models.VisionRequest) (models.VisionOutcome, error)
}

type PDFExtractorConfig struct {
	VisionModel         string
	VisionPromptVersion string
	MaxVisionPages      int
}

type PDFDocument struct {
	ID                 string
	Payload            []byte
	ExtractionConfigID string
}

type PDFExtractor struct {
	media  VisionModels
	config PDFExtractorConfig
}

func NewPDFExtractor(media VisionModels, config PDFExtractorConfig) *PDFExtractor {
	config.VisionModel = strings.TrimSpace(config.VisionModel)
	config.VisionPromptVersion = strings.TrimSpace(config.VisionPromptVersion)
	if config.MaxVisionPages == 0 {
		config.MaxVisionPages = 20
	}
	return &PDFExtractor{media: media, config: config}
}

func (e *PDFExtractor) Extract(ctx context.Context, document PDFDocument, rendered documentrender.Result) (normalize.Artifact, error) {
	missing, err := normalize.PDFPagesRequiringVision(document.Payload)
	if err != nil {
		return normalize.Artifact{}, err
	}
	input := normalize.Input{
		SourceID: strings.TrimSpace(document.ID), ExtractionConfigID: strings.TrimSpace(document.ExtractionConfigID),
		Format: "pdf", Payload: document.Payload,
	}
	if len(missing) == 0 {
		return normalize.PDF(input)
	}
	if e == nil || e.media == nil || e.config.VisionModel == "" || e.config.VisionPromptVersion == "" ||
		e.config.MaxVisionPages < 1 || len(missing) > e.config.MaxVisionPages {
		return normalize.Artifact{}, normalize.ErrProcessingBudget
	}

	assets := make(map[int]documentrender.Asset, len(rendered.Assets))
	for _, asset := range rendered.Assets {
		if asset.Page.Ordinal < 1 {
			return normalize.Artifact{}, errors.New("rendered PDF page identity is invalid")
		}
		if _, duplicate := assets[asset.Page.Ordinal]; duplicate {
			return normalize.Artifact{}, errors.New("rendered PDF page identity is duplicated")
		}
		assets[asset.Page.Ordinal] = asset
	}
	visualPages := make([]normalize.VisualPage, 0, len(missing))
	for _, ordinal := range missing {
		asset, ok := assets[ordinal]
		if !ok {
			return normalize.Artifact{}, errors.New("rendered PDF page is missing")
		}
		outcome, err := e.media.DescribeImage(ctx, models.VisionRequest{
			Model: e.config.VisionModel, MediaType: "image/png", Image: asset.Payload,
			Width: asset.Page.Width, Height: asset.Page.Height, PromptVersion: e.config.VisionPromptVersion,
		})
		if err != nil {
			return normalize.Artifact{}, err
		}
		regions := make([]normalize.ImageRegion, 0, len(outcome.Regions))
		for _, region := range outcome.Regions {
			regions = append(regions, normalize.ImageRegion{
				Text: region.Text, X: region.X, Y: region.Y, Width: region.Width, Height: region.Height,
			})
		}
		visualPages = append(visualPages, normalize.VisualPage{
			Ordinal: ordinal, Width: asset.Page.Width, Height: asset.Page.Height, Regions: regions,
		})
	}
	return normalize.PDFWithVisualPages(input, visualPages)
}
