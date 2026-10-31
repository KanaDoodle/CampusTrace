package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type versionExtractor struct{ calls atomic.Int32 }

func (e *versionExtractor) Claims(ctx context.Context, text string) ([]d.Claim, error) {
	e.calls.Add(1)
	return Extract(ctx, text)
}

func TestAnalysisSemanticVersionCheckedBeforeExtractor(t *testing.T) {
	extractor := &versionExtractor{}
	service := &Service{Root: context.Background(), Instance: "version-fixture", Extractor: extractor}
	var response Response
	err := service.AnalyzeObservation(&Request{Text: "tech: go", AnalysisVersion: "claims-v3-semantics"}, &response)
	if err != nil || response.Error == "" || response.AnalysisVersion != d.AnalysisVersion || extractor.calls.Load() != 0 {
		t.Fatal("incompatible request invoked extractor", response, err)
	}
	response = Response{}
	err = service.AnalyzeObservation(&Request{Text: "tech: go", AnalysisVersion: d.AnalysisVersion}, &response)
	if err != nil || response.Error != "" || response.AnalysisVersion != d.AnalysisVersion || extractor.calls.Load() != 1 {
		t.Fatal("matching version rejected", response, err)
	}
	// Legacy callers may omit the new field; the server still identifies the
	// actual implementation. New callers always send and validate the version.
	response = Response{}
	err = service.AnalyzeObservation(&Request{Text: "tech: go"}, &response)
	if err != nil || response.Error != "" || response.AnalysisVersion != d.AnalysisVersion || extractor.calls.Load() != 2 {
		t.Fatal("legacy request compatibility lost", response, err)
	}
}

func TestAnalysisResponseRejectsUnversionedOrOlderServers(t *testing.T) {
	request := Request{Text: "tech: go", AnalysisVersion: d.AnalysisVersion}
	var old Response
	if err := json.Unmarshal([]byte(`{"claims":[{"type":"TECH_STACK","value":"go","excerpt":"tech: go","extraction_method":"RULE","confidence":1}],"instance":"legacy"}`), &old); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(validateResponse(request, old), ErrSemanticVersion) {
		t.Fatal("unversioned response could contaminate current cache")
	}
	old.AnalysisVersion = "claims-v3-semantics"
	if !errors.Is(validateResponse(request, old), ErrSemanticVersion) {
		t.Fatal("older server accepted")
	}
	old.AnalysisVersion = d.AnalysisVersion
	if err := validateResponse(request, old); err != nil {
		t.Fatal(err)
	}
}
