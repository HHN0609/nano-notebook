package obsbench_test

import (
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/obsbench"
)

func TestManifestDigestFreezesIdenticalInputsAcrossStages(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	manifest, err := obsbench.NewManifest(obsbench.ReferenceWorkloadV1(), "primary-1m-v1", 1_000_000, base)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, firstDigest, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, secondDigest, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) || firstDigest != secondDigest || len(firstDigest) != 64 {
		t.Fatalf("manifest is unstable: first=%q/%q second=%q/%q", firstJSON, firstDigest, secondJSON, secondDigest)
	}

	changed, err := obsbench.NewManifest(obsbench.ReferenceWorkloadV1(), "primary-1m-v1", 1_000_001, base)
	if err != nil {
		t.Fatal(err)
	}
	_, changedDigest, err := changed.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if changedDigest == firstDigest {
		t.Fatal("dataset cardinality did not change the manifest digest")
	}
}
