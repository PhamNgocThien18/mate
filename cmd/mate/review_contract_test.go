package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/store"
)

const supportedReview = "## Summary\nChecked the contract.\n## Verdict\npass\n## Findings\nnone\n## Verification gaps\nnone\n"

func TestReviewCheckRequiresUnambiguousEvidenceForNewReviews(t *testing.T) {
	t.Parallel()
	f := newSinceFixture(t, priorReportWithFindings)
	reviewer := "review-current"
	recordReview(t, f.diffFixture, reviewer, supportedReview)
	path := f.w.CrewReviewMeta("shop", reviewer)
	meta, err := store.ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, contract, report string
		accepted               bool
	}{
		{"supported", store.ReviewContract, supportedReview, true},
		{"notes do not block", store.ReviewContract, supportedReview + "## Notes\n- Consider renaming the helper.\n", true},
		{"material defect", store.ReviewContract, strings.Replace(supportedReview, "## Findings\nnone", "## Findings\n- store.go:12 loses persisted state", 1), false},
		{"skipped live proof", store.ReviewContract, strings.Replace(supportedReview, "## Verification gaps\nnone", "## Verification gaps\n- Live read skipped: credentials unavailable", 1), false},
		{"missing gaps", store.ReviewContract, strings.Replace(supportedReview, "## Verification gaps\nnone\n", "", 1), false},
		{"missing findings", store.ReviewContract, strings.Replace(supportedReview, "## Findings\nnone\n", "", 1), false},
		{"duplicate findings", store.ReviewContract, supportedReview + "## Findings\n- missed bug\n", false},
		{"duplicate gaps", store.ReviewContract, supportedReview + "## Verification gaps\n- not checked\n", false},
		{"duplicate verdict", store.ReviewContract, supportedReview + "## Verdict\nincomplete\n", false},
		{"fenced verdict", store.ReviewContract, "```md\n## Verdict\npass\n```\n## Summary\nUnfinished\n## Findings\nnone\n## Verification gaps\nnone\n", false},
		{"fenced example in notes", store.ReviewContract, supportedReview + "## Notes\n```md\n## Findings\n- example\n```\n", true},
		{"unknown contract", "999", supportedReview, false},
		{"legacy report remains valid", "", "## Summary\nDone\n## Verdict\npass\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta[store.MetaReviewContract] = tc.contract
			if err := store.WriteMeta(path, meta); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.w.CrewReport("shop", reviewer), []byte(tc.report), 0644); err != nil {
				t.Fatal(err)
			}
			_, _, err := checkReview(context.Background(), f.w, gitx.New(), "shop", "k3", reviewer)
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted=%v, error=%v", tc.accepted, err)
			}
		})
	}
}

func TestReviewCheckRequiresExactlyOneClosurePerPriorFinding(t *testing.T) {
	t.Parallel()
	f := newSinceFixture(t, priorReportWithFindings)
	reviewer := "review-current"
	recordReview(t, f.diffFixture, reviewer, supportedReview)
	path := f.w.CrewReviewMeta("shop", reviewer)
	meta, err := store.ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := reviewSince(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3", meta)
	if err != nil {
		t.Fatal(err)
	}
	scope.record(meta)
	meta[store.MetaReviewContract] = store.ReviewContract
	if err := store.WriteMeta(path, meta); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		marks    string
		accepted bool
	}{
		{"- F1 closed: observed probe\n- F2 closed: static path", true},
		{"- F1 closed: observed probe\n  - output: correct\n- F2 closed: static path", true},
		{"- F1 open: still fails\n- F1 closed: later claim\n- F2 closed: static path", false},
		{"- F1 closed: probe\n- F2 closed: static\n- F3 closed: invented", false},
		{"- F1 closed: probe", false},
		{"- F1 closed: probe\n- F2 open: still fails", false},
		{"- F1 closed: probe\n- F2 closed: static\n## Prior findings\nnone", false},
	} {
		report := supportedReview + "## Prior findings\n" + tc.marks + "\n"
		if err := os.WriteFile(f.w.CrewReport("shop", reviewer), []byte(report), 0644); err != nil {
			t.Fatal(err)
		}
		_, _, err := checkReview(context.Background(), f.w, gitx.New(), "shop", "k3", reviewer)
		if (err == nil) != tc.accepted {
			t.Errorf("marks=%q accepted=%v error=%v", tc.marks, tc.accepted, err)
		}
	}
}
